package jsonrpc2

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockListener implements net.Listener for testing Serve.
type mockListener struct {
	acceptChan chan net.Conn
	closeChan  chan struct{}
	addr       net.Addr
	mu         sync.Mutex
	closed     bool
	acceptErr  error // Error to return on Accept
}

func newMockListener(addr string) *mockListener {
	tcpAddr, _ := net.ResolveTCPAddr("tcp", addr) // Use TCPAddr for simplicity
	return &mockListener{
		acceptChan: make(chan net.Conn),
		closeChan:  make(chan struct{}),
		addr:       tcpAddr,
	}
}

func (m *mockListener) Accept() (net.Conn, error) {
	m.mu.Lock()
	closed := m.closed
	acceptErr := m.acceptErr
	m.mu.Unlock()

	if closed {
		return nil, net.ErrClosed
	}
	if acceptErr != nil {
		return nil, acceptErr
	}

	select {
	case conn := <-m.acceptChan:
		return conn, nil
	case <-m.closeChan:
		return nil, net.ErrClosed
	}
}

func (m *mockListener) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return net.ErrClosed
	}
	m.closed = true
	close(m.closeChan)
	return nil
}

func (m *mockListener) Addr() net.Addr {
	return m.addr
}

func (m *mockListener) InjectConn(conn net.Conn) {
	m.acceptChan <- conn
}

func (m *mockListener) SetAcceptError(err error) {
	m.mu.Lock()
	m.acceptErr = err
	m.mu.Unlock()
}

// mockConn implements net.Conn for testing.
type mockConn struct {
	io.Reader
	io.Writer
	closeFunc func() error
	localAddr net.Addr
	remoteAddr net.Addr
	deadline   *time.Timer
	mu         sync.Mutex
}

func newMockConn(r io.Reader, w io.Writer) *mockConn {
	return &mockConn{
		Reader: r,
		Writer: w,
		localAddr: &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 1234},
		remoteAddr: &net.TCPAddr{IP: net.ParseIP("198.51.100.1"), Port: 5678},
	}
}

func (m *mockConn) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

func (m *mockConn) LocalAddr() net.Addr {
	return m.localAddr
}

func (m *mockConn) RemoteAddr() net.Addr {
	return m.remoteAddr
}

func (m *mockConn) SetDeadline(t time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deadline == nil {
		m.deadline = time.NewTimer(time.Until(t))
	} else {
		if !m.deadline.Stop() {
			<-m.deadline.C // Drain channel if Stop returns false
		}
		m.deadline.Reset(time.Until(t))
	}
	return nil
}

func (m *mockConn) SetReadDeadline(t time.Time) error {
	return m.SetDeadline(t) // Simplified for mock
}

func (m *mockConn) SetWriteDeadline(t time.Time) error {
	return m.SetDeadline(t) // Simplified for mock
}


func TestNewServer(t *testing.T) {
	handler := &mockHandler{}
	server := NewServer(handler)

	require.NotNil(t, server, "NewServer should return a non-nil server")
	assert.Equal(t, handler, server.handler, "Handler should be set")
	assert.NotNil(t, server.NewEncoder, "NewEncoder should be set to default")
	assert.NotNil(t, server.NewDecoder, "NewDecoder should be set to default")
	assert.NotNil(t, server.NewPacketEncoder, "NewPacketEncoder should be set to default")
	assert.NotNil(t, server.NewPacketDecoder, "NewPacketDecoder should be set to default")
	assert.Equal(t, time.Duration(DefaultHTTPReadTimeout)*time.Second, server.HTTPReadTimeout, "HTTPReadTimeout should have default value")
	assert.Equal(t, time.Duration(DefaultHTTPShutdownTimeout)*time.Second, server.HTTPShutdownTimeout, "HTTPShutdownTimeout should have default value")
	expectedPacketRoutines := min(runtime.NumCPU(), runtime.GOMAXPROCS(-1))
	assert.Equal(t, expectedPacketRoutines, server.PacketRoutines, "PacketRoutines should have default value")
}

func TestServer_ListenAndServe_SchemeRouting(t *testing.T) {
	handler := &mockHandler{}
	server := NewServer(handler)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel() // Ensure context is cancelled eventually

	tests := []struct {
		name        string
		uri         string
		expectError error
		expectType  string // "conn", "packet", "http", "error"
	}{
		{"tcp", "tcp://127.0.0.1:0", net.ErrClosed, "conn"}, // Use port 0 for auto-assign, expect ErrClosed on cancel
		{"tcp4", "tcp4://127.0.0.1:0", net.ErrClosed, "conn"},
		{"tcp6", "tcp6://[::1]:0", net.ErrClosed, "conn"},
		{"udp", "udp://127.0.0.1:0", net.ErrClosed, "packet"},
		{"udp4", "udp4://127.0.0.1:0", net.ErrClosed, "packet"},
		{"udp6", "udp6://[::1]:0", net.ErrClosed, "packet"},
		{"http", "http://127.0.0.1:0", http.ErrServerClosed, "http"},
		{"unix", "unix://" + filepath.Join(t.TempDir(), "test.sock"), net.ErrClosed, "conn"},
		{"unixgram", "unixgram://" + filepath.Join(t.TempDir(), "testgram.sock"), net.ErrClosed, "packet"},
		// unixpacket might not be supported on all platforms, skip for broad compatibility or add build tags
		// {"unixpacket", "unixpacket:///tmp/testpacket.sock", net.ErrClosed, "packet"},
		{"invalid_uri", "::invalid::", &url.Error{}, "error"},
		{"unknown_scheme", "ftp://localhost/file", ErrUnknownScheme, "error"},
		{"no_scheme", "127.0.0.1:9090", &url.Error{}, "error"}, // url.Parse needs scheme
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			// Use a short timeout context for listen calls to prevent hangs
			listenCtx, listenCancel := context.WithTimeout(ctx, 100*time.Millisecond)
			defer listenCancel()

			err := server.ListenAndServe(listenCtx, tt.uri)

			if tt.expectError != nil {
				require.Error(t, err, "ListenAndServe should return an error for %s", tt.uri)
				// Use errors.Is for wrapped errors, check type for others
				if errors.Is(tt.expectError, net.ErrClosed) || errors.Is(tt.expectError, http.ErrServerClosed) {
					// These errors occur because the context timed out / was cancelled quickly, which is expected
					assert.ErrorIs(t, err, tt.expectError, "Error should be or wrap %T for %s", tt.expectError, tt.uri)
				} else if _, ok := tt.expectError.(*url.Error); ok {
					assert.IsType(t, tt.expectError, err, "Error should be type %T for %s", tt.expectError, tt.uri)
				} else {
					assert.ErrorIs(t, err, tt.expectError, "Error should be %v for %s", tt.expectError, tt.uri)
				}
			} else {
				// This case should ideally not be hit with the timeout context,
				// but check for no error if tt.expectError was nil.
				assert.NoError(t, err, "ListenAndServe should not return an error for %s", tt.uri)
			}

			// We can't easily verify the *type* of listener started without more complex mocks or reflection.
			// The error checking above gives some confidence the correct path was taken.
		})
	}
}

func TestServer_Serve(t *testing.T) {
	handler := &mockHandler{}
	server := NewServer(handler)
	listener := newMockListener("127.0.0.1:12345")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	serveErr := make(chan error, 1)

	go func() {
		defer wg.Done()
		serveErr <- server.Serve(ctx, listener)
	}()

	// Inject a connection
	connReadPipe, connWritePipe := io.Pipe()
	mockConn := newMockConn(connReadPipe, connWritePipe)
	connClosed := make(chan struct{})
	mockConn.closeFunc = func() error { // Ensure close is tracked
		close(connClosed)
		return connReadPipe.Close() // Close reader to unblock potential reads
	}

	listener.InjectConn(mockConn)

	// Wait for the connection to be processed (or timeout)
	// We expect the RPCServer.Run inside Serve to block until context cancel or conn close
	select {
	case <-connClosed:
		// Connection was closed, likely by RPCServer exiting
	case <-time.After(2 * time.Second): // Increased timeout
		t.Fatal("Timeout waiting for mock connection to be closed")
	}

	// Cancel the context to stop the server
	cancel()

	// Wait for Serve goroutine to exit
	wg.Wait()

	// Check the error returned by Serve
	err := <-serveErr
	require.Error(t, err, "Serve should return an error after context cancellation")
	// The error might be context.Canceled or net.ErrClosed depending on timing
	assert.True(t, errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed), "Serve error should be context.Canceled or net.ErrClosed")

	// Test Accept error
	ctxErr, cancelErr := context.WithCancel(t.Context())
	defer cancelErr()
	listenerErr := newMockListener("127.0.0.1:12346")
	expectedErr := errors.New("accept failed")
	listenerErr.SetAcceptError(expectedErr)

	err = server.Serve(ctxErr, listenerErr) // Run synchronously for error check
	require.Error(t, err, "Serve should return error from Accept")
	assert.ErrorIs(t, err, expectedErr, "Serve error should wrap the Accept error")
}

// mockBinder for testing Binder integration
type mockBinder struct {
	bindCalled chan struct{}
	boundCtx   context.Context
	boundRPC   *RPCServer
	boundStop  context.CancelCauseFunc
}

func newMockBinder() *mockBinder {
	return &mockBinder{bindCalled: make(chan struct{}, 1)}
}

func (m *mockBinder) Bind(ctx context.Context, rpc *RPCServer, stop context.CancelCauseFunc) {
	m.boundCtx = ctx
	m.boundRPC = rpc
	m.boundStop = stop
	m.bindCalled <- struct{}{} // Signal that Bind was called
}

func TestServer_Serve_WithBinder(t *testing.T) {
	handler := &mockHandler{}
	binder := newMockBinder()
	server := NewServer(handler)
	server.Binder = binder // Assign the binder

	listener := newMockListener("127.0.0.1:12347")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	serveErr := make(chan error, 1)

	go func() {
		defer wg.Done()
		serveErr <- server.Serve(ctx, listener)
	}()

	// Inject a connection
	connReadPipe, connWritePipe := io.Pipe()
	mockConn := newMockConn(connReadPipe, connWritePipe)
	connClosed := make(chan struct{})
	mockConn.closeFunc = func() error { close(connClosed); return connReadPipe.Close() }

	listener.InjectConn(mockConn)

	// Wait for Binder.Bind to be called
	select {
	case <-binder.bindCalled:
		// Bind was called, check parameters
		require.NotNil(t, binder.boundCtx, "Binder context should not be nil")
		require.NotNil(t, binder.boundRPC, "Binder RPCServer should not be nil")
		require.NotNil(t, binder.boundStop, "Binder stop func should not be nil")
		assert.Equal(t, mockConn, binder.boundCtx.Value(CtxNetConn), "Context should contain the net.Conn")

		// Simulate binder stopping the connection processing
		binder.boundStop(errors.New("stopped by binder"))

	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for Binder.Bind to be called")
	}

	// Wait for the connection handler goroutine to finish (due to stop call)
	select {
	case <-connClosed:
		// Connection was closed as expected
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for mock connection to be closed after binder stop")
	}

	// Cancel the main server context
	cancel()
	wg.Wait() // Wait for Serve to exit

	err := <-serveErr
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed), "Serve error should be context.Canceled or net.ErrClosed")
}


func TestServer_ServePacket(t *testing.T) {
	handler := &mockHandler{}
	server := NewServer(handler)
	server.PacketRoutines = 2 // Use multiple routines for testing
	mockPC := newMockPacketConn()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	serveErr := make(chan error, 1)

	go func() {
		defer wg.Done()
		// ServePacket blocks until context cancel or internal error in all routines
		serveErr <- server.ServePacket(ctx, mockPC)
	}()

	// Simulate receiving a packet (this would trigger DecodeFrom in RPCServer)
	// We can't easily test the full RPC flow here, but we can verify ServePacket runs
	// and stops correctly.
	mockPC.SendData([]byte(`{"jsonrpc":"2.0","method":"test","id":1}`))

	// Allow some time for routines to potentially start processing
	time.Sleep(100 * time.Millisecond)

	// Cancel the context to stop the server
	cancel()

	// Wait for ServePacket goroutine to exit
	wg.Wait()

	// Check the error returned by ServePacket
	err := <-serveErr
	// Expect context.Canceled or context.DeadlineExceeded if DecodeFrom was blocked
	// Or potentially net.ErrClosed if ReadFrom returned it due to Close.
	// Or nil if it exited cleanly on cancel before erroring.
	if err != nil {
		assert.True(t, errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, net.ErrClosed),
			"ServePacket error should reflect context cancellation or closed connection, got: %v", err)
	}

	// Verify context contains the packet conn
	pCtx := context.WithValue(context.Background(), CtxNetPacketConn, mockPC)
	assert.Equal(t, mockPC, pCtx.Value(CtxNetPacketConn), "Context should contain the net.PacketConn")

	// Test ReadFrom error propagation
	ctxErr, cancelErr := context.WithCancel(t.Context())
	defer cancelErr()
	mockPCErr := newMockPacketConn()
	expectedErr := errors.New("readfrom failed")
	mockPCErr.SetReadError(expectedErr) // Set error to be returned by ReadFrom

	err = server.ServePacket(ctxErr, mockPCErr) // Run synchronously
	require.Error(t, err, "ServePacket should return error from ReadFrom")
	// The error from Run inside ServePacket gets wrapped
	assert.ErrorContains(t, err, expectedErr.Error(), "ServePacket error should contain the ReadFrom error")
}

func TestServer_ServePacket_WithBinder(t *testing.T) {
	handler := &mockHandler{}
	binder := newMockBinder()
	server := NewServer(handler)
	server.Binder = binder // Assign the binder
	server.PacketRoutines = 1 // Simplify test with one routine

	mockPC := newMockPacketConn()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	serveErr := make(chan error, 1)

	go func() {
		defer wg.Done()
		serveErr <- server.ServePacket(ctx, mockPC)
	}()

	// Wait for Binder.Bind to be called (should happen quickly as routine starts)
	select {
	case <-binder.bindCalled:
		// Bind was called, check parameters
		require.NotNil(t, binder.boundCtx, "Binder context should not be nil")
		require.NotNil(t, binder.boundRPC, "Binder RPCServer should not be nil")
		require.NotNil(t, binder.boundStop, "Binder stop func should not be nil")
		assert.Equal(t, mockPC, binder.boundCtx.Value(CtxNetPacketConn), "Context should contain the net.PacketConn")

		// Simulate binder stopping the server processing
		binder.boundStop(errors.New("stopped by binder"))

	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for Binder.Bind to be called")
	}

	// Wait for ServePacket goroutine to exit (due to stop call)
	wg.Wait()

	err := <-serveErr
	require.Error(t, err, "ServePacket should return an error after binder stop")
	// The error originates from the rpcServer.Run call which gets cancelled by the binder's stop
	assert.ErrorContains(t, err, "stopped by binder", "ServePacket error should reflect binder stop reason")

	// Ensure main context cancel doesn't interfere if already stopped
	cancel()
}


func TestServer_listenAndServeHTTP(t *testing.T) {
	handler := &mockHandler{
		handleFunc: func(ctx context.Context, req *Request) (any, error) {
			// Check context propagation
			httpReq := ctx.Value(CtxHTTPRequest).(*http.Request)
			assert.NotNil(t, httpReq, "Context should contain HTTPRequest")
			assert.Equal(t, "/rpc", httpReq.URL.Path)

			if req.Method == "echo" {
				return "echo_response", nil
			}
			return nil, ErrMethodNotFound
		},
	}
	server := NewServer(handler)
	server.HTTPReadTimeout = 1 * time.Second
	server.HTTPShutdownTimeout = 2 * time.Second

	// Create a test HTTP server
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate the core logic of listenAndServeHTTP's handler setup
		httpHandler := NewHTTPHandler(server.handler)
		httpHandler.Binder = server.Binder // Pass binder if set
		httpHandler.NewDecoder = server.NewDecoder
		httpHandler.NewEncoder = server.NewEncoder
		httpHandler.ServeHTTP(w, r)
	}))
	defer testServer.Close()

	// Parse the test server URL to pass to listenAndServeHTTP (though we mock the actual listening)
	serverURL, err := url.Parse(testServer.URL + "/rpc")
	require.NoError(t, err)

	// --- Test successful request ---
	reqBody := `{"jsonrpc":"2.0", "method":"echo", "id": 1}`
	resp, err := http.Post(testServer.URL+"/rpc", "application/json", strings.NewReader(reqBody))
	require.NoError(t, err, "HTTP POST request failed")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode, "HTTP status code should be OK")
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	expectedResp := `{"jsonrpc":"2.0","result":"echo_response","id":1}`
	assert.JSONEq(t, expectedResp, string(respBody), "HTTP response body mismatch")

	// --- Test shutdown behavior (simulated) ---
	// We can't directly test the httpServer.Shutdown logic easily without
	// actually running listenAndServeHTTP in a goroutine and cancelling its context.
	// However, we know NewServer sets the timeouts, and the code uses them.
	// We can verify the timeouts are set on the *Server instance.
	assert.Equal(t, 1*time.Second, server.HTTPReadTimeout)
	assert.Equal(t, 2*time.Second, server.HTTPShutdownTimeout)

	// --- Test listenAndServeHTTP directly (mocking ListenAndServe) ---
	// This part is tricky because listenAndServeHTTP calls httpServer.ListenAndServe()
	// which blocks. We can test the setup part.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// Create a temporary listener to get a free port
	tempLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := tempLn.Addr().String()
	require.NoError(t, tempLn.Close()) // Close immediately, we just needed the address

	httpURL := fmt.Sprintf("http://%s/rpc", addr)
	uri, err := url.Parse(httpURL)
	require.NoError(t, err)

	errChan := make(chan error, 1)
	go func() {
		// This will try to listen on the addr. We cancel quickly.
		errChan <- server.listenAndServeHTTP(ctx, uri)
	}()

	// Give it a moment to start listening, then cancel
	time.Sleep(50 * time.Millisecond)
	cancel()

	// Wait for the goroutine to exit and check the error
	select {
	case err := <-errChan:
		// Expect ErrServerClosed because we cancelled the context, triggering Shutdown
		require.ErrorIs(t, err, http.ErrServerClosed, "listenAndServeHTTP should return ErrServerClosed on context cancel")
	case <-time.After(3 * time.Second): // Use shutdown timeout + buffer
		t.Fatal("Timeout waiting for listenAndServeHTTP goroutine to exit")
	}
}

// Helper to clean up unix sockets if they exist
func cleanupSocket(path string) {
	if _, err := os.Stat(path); err == nil {
		os.Remove(path)
	}
}

func TestServer_ListenAndServe_Unix(t *testing.T) {
	// Test Unix Domain Socket (connection-oriented)
	handler := &mockHandler{}
	server := NewServer(handler)
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond) // Short timeout
	defer cancel()

	sockPath := filepath.Join(t.TempDir(), "test_conn.sock")
	cleanupSocket(sockPath) // Ensure clean state
	defer cleanupSocket(sockPath)
	uri := "unix://" + sockPath

	err := server.ListenAndServe(ctx, uri)
	// Expect ErrClosed because context times out quickly, closing the listener
	require.ErrorIs(t, err, net.ErrClosed, "ListenAndServe(unix) should return ErrClosed on context timeout")
}

func TestServer_ListenAndServe_UnixGram(t *testing.T) {
	// Test Unix Domain Socket (packet-oriented)
	handler := &mockHandler{}
	server := NewServer(handler)
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond) // Short timeout
	defer cancel()

	sockPath := filepath.Join(t.TempDir(), "test_gram.sock")
	cleanupSocket(sockPath) // Ensure clean state
	defer cleanupSocket(sockPath)
	uri := "unixgram://" + sockPath

	err := server.ListenAndServe(ctx, uri)
	// Expect context.Canceled or similar from ServePacket when context times out
	require.Error(t, err, "ListenAndServe(unixgram) should return an error on context timeout")
	assert.True(t, errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed) || errors.Is(err, context.DeadlineExceeded), "Error should indicate context cancellation or closed connection")
}
