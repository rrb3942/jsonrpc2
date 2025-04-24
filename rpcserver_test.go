package jsonrpc2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mocks ---

// mockReadWriter simulates an io.ReadWriter for stream tests.
type mockReadWriter struct {
	reader *bytes.Buffer
	writer *bytes.Buffer
	mu     sync.Mutex
	closed bool
}

func newMockReadWriter(input []byte) *mockReadWriter {
	return &mockReadWriter{
		reader: bytes.NewBuffer(input),
		writer: bytes.NewBuffer(nil),
	}
}

func (m *mockReadWriter) Read(p []byte) (n int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, io.EOF // Or net.ErrClosed, depending on desired simulation
	}
	return m.reader.Read(p)
}

func (m *mockReadWriter) Write(p []byte) (n int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, errors.New("write on closed writer")
	}
	return m.writer.Write(p)
}

func (m *mockReadWriter) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("already closed")
	}
	m.closed = true
	return nil
}

func (m *mockReadWriter) Output() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.writer.Bytes()
}

// --- Test Helpers ---

func runServerWithTimeout(t *testing.T, rp *RPCServer, duration time.Duration) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	return rp.Run(ctx)
}

func assertJSONMatch(t *testing.T, expected, actual []byte) {
	t.Helper()
	var exp, act any
	err := json.Unmarshal(expected, &exp)
	require.NoError(t, err, "Failed to unmarshal expected JSON")
	err = json.Unmarshal(actual, &act)
	require.NoError(t, err, "Failed to unmarshal actual JSON: %s", string(actual)) // Added actual content on error
	assert.Equal(t, exp, act, "JSON mismatch")
}

// --- Tests ---

func TestNewStreamServer(t *testing.T) {
	t.Parallel()
	handler := &mockHandler{}
	rw := newMockReadWriter(nil)
	decoder := NewDecoder(rw)
	encoder := NewEncoder(rw)
	rp := NewStreamServer(decoder, encoder, handler)
	require.NotNil(t, rp)
	assert.Equal(t, handler, rp.Handler)
	assert.NotNil(t, rp.decoder)
	assert.NotNil(t, rp.encoder)
	assert.False(t, rp.SerialBatch)
	assert.False(t, rp.NoRoutines)
	assert.False(t, rp.WaitOnClose)
	assert.NotNil(t, rp.Callbacks.OnHandlerPanic) // Default should be set
}

func TestNewStreamServerFromIO(t *testing.T) {
	t.Parallel()
	handler := &mockHandler{}
	rw := newMockReadWriter(nil)
	rp := NewStreamServerFromIO(rw, handler)
	require.NotNil(t, rp)
	assert.Equal(t, handler, rp.Handler)
	assert.NotNil(t, rp.decoder)
	assert.NotNil(t, rp.encoder)
}

func TestNewPacketServer(t *testing.T) {
	t.Parallel()
	handler := &mockHandler{}
	conn := newMockPacketConn()
	decoder := NewPacketDecoder(conn)
	encoder := NewPacketEncoder(conn)
	rp := NewPacketServer(decoder, encoder, handler)
	require.NotNil(t, rp)
	assert.Equal(t, handler, rp.Handler)
	assert.NotNil(t, rp.decoder)
	assert.NotNil(t, rp.encoder)
}

func TestNewRPCServerFromPacket(t *testing.T) {
	t.Parallel()
	handler := &mockHandler{}
	conn := newMockPacketConn()
	rp := NewRPCServerFromPacket(conn, handler)
	require.NotNil(t, rp)
	assert.Equal(t, handler, rp.Handler)
	assert.NotNil(t, rp.decoder)
	assert.NotNil(t, rp.encoder)
}

func TestRPCServer_Run_SingleRequest_Stream(t *testing.T) {
	t.Parallel()
	handler := &mockHandler{}
	input := `{"jsonrpc": "2.0", "method": "testMethod", "id": 1}` + "\n"
	rw := newMockReadWriter([]byte(input))
	rp := NewStreamServerFromIO(rw, handler)

	err := runServerWithTimeout(t, rp, 100*time.Millisecond)
	assert.ErrorIs(t, err, io.EOF, "Server should have read to EOF")

	expectedOutput := `{"jsonrpc":"2.0","result":"handled testMethod","id":1}`
	assert.JSONEq(t, string(expectedOutput), string(rw.Output()))
}

func TestRPCServer_Run_SingleRequest_Packet(t *testing.T) {
	t.Parallel()
	handler := &mockHandler{}
	conn := newMockPacketConn()
	rp := NewRPCServerFromPacket(conn, handler)

	input := `{"jsonrpc": "2.0", "method": "packetTest", "id": "abc"}`
	conn.SendData([]byte(input)) // Simulate receiving a packet

	err := runServerWithTimeout(t, rp, 100*time.Millisecond)
	assert.ErrorIs(t, err, context.DeadlineExceeded, "Server should stop due to timeout")

	select {
	case output := <-conn.writeChan:
		expectedOutput := `{"jsonrpc":"2.0","result":"handled packetTest","id":"abc"}`
		assert.JSONEq(t, string(expectedOutput), string(output))
	case <-time.After(50 * time.Millisecond):
		t.Fatal("Did not receive response from packet server")
	}
}

func TestRPCServer_Run_Notification_Stream(t *testing.T) {
	t.Parallel()
	handler := &mockHandler{}
	// Notification has no ID
	input := `{"jsonrpc": "2.0", "method": "notifyMethod"}` + "\n"
	rw := newMockReadWriter([]byte(input))
	rp := NewStreamServerFromIO(rw, handler)

	err := runServerWithTimeout(t, rp, 100*time.Millisecond)
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	// No response should be written for a notification
	assert.Empty(t, rw.Output(), "Should not write a response for a notification")
}

func TestRPCServer_Run_BatchRequest_Stream_Parallel(t *testing.T) {
	t.Parallel()
	handler := &mockHandler{
		handleFunc: func(ctx context.Context, req *Request) (any, error) {
			// Introduce slight delay to test parallelism
			time.Sleep(10 * time.Millisecond)
			idVal, _ := req.ID.Int64() // Get int64 ID for map key
			return fmt.Sprintf("handled %s with id %d", req.Method, idVal), nil
		},
	}
	input := `[
		{"jsonrpc": "2.0", "method": "batch1", "id": 10},
		{"jsonrpc": "2.0", "method": "batch2", "id": 11},
		{"jsonrpc": "2.0", "method": "notifyBatch"}
	]` + "\n" // Notification in batch
	rw := newMockReadWriter([]byte(input))
	rp := NewStreamServerFromIO(rw, handler)
	rp.SerialBatch = false // Explicitly parallel (default)

	startTime := time.Now()
	err := runServerWithTimeout(t, rp, 100*time.Millisecond)
	duration := time.Since(startTime)

	assert.ErrorIs(t, err, context.DeadlineExceeded)
	// Check if it ran somewhat concurrently (less than 2*10ms + overhead)
	assert.Less(t, duration, 30*time.Millisecond, "Batch processing took too long, likely serial")

	// Note: Order in response batch is not guaranteed by spec unless requests are processed serially.
	// We need to unmarshal and check presence, not exact string match.
	var responses []Response
	err = json.Unmarshal(rw.Output(), &responses)
	require.NoError(t, err, "Failed to unmarshal batch response: %s", string(rw.Output()))
	require.Len(t, responses, 2, "Expected 2 responses (notification is ignored)")

	results := make(map[int64]string) // Use int64 for key
	for _, resp := range responses {
		numID, idErr := resp.ID.Int64() // Assuming integer IDs for simplicity here
		require.NoError(t, idErr, "Failed to get int64 ID from response")
		strResult, ok := resp.Result.value.(string)
		require.True(t, ok, "Result value is not a string")
		results[numID] = strResult
	}

	assert.Equal(t, "handled batch1 with id 10", results[int64(10)])
	assert.Equal(t, "handled batch2 with id 11", results[int64(11)])
}

func TestRPCServer_Run_BatchRequest_Stream_Serial(t *testing.T) {
	t.Parallel()
	var callOrder []int64
	var mu sync.Mutex
	handler := &mockHandler{
		handleFunc: func(ctx context.Context, req *Request) (any, error) {
			id, _ := req.ID.Int64()
			mu.Lock()
			callOrder = append(callOrder, id)
			mu.Unlock()
			time.Sleep(10 * time.Millisecond) // Delay to check serial execution
			return fmt.Sprintf("handled %s with id %d", req.Method, id), nil
		},
	}
	input := `[
		{"jsonrpc": "2.0", "method": "batch1", "id": 10},
		{"jsonrpc": "2.0", "method": "batch2", "id": 11}
	]` + "\n"
	rw := newMockReadWriter([]byte(input))
	rp := NewStreamServerFromIO(rw, handler)
	rp.SerialBatch = true // Force serial execution

	startTime := time.Now()
	err := runServerWithTimeout(t, rp, 100*time.Millisecond)
	duration := time.Since(startTime)

	assert.ErrorIs(t, err, io.EOF)
	// Check if it ran serially (at least 2*10ms + overhead)
	assert.GreaterOrEqual(t, duration, 20*time.Millisecond, "Batch processing finished too quickly, likely parallel")

	// Order must be preserved in serial execution
	expectedOutput := `[
		{"jsonrpc":"2.0","result":"handled batch1 with id 10","id":10},
		{"jsonrpc":"2.0","result":"handled batch2 with id 11","id":11}
	]`
	assert.JSONEq(t, string(expectedOutput), string(rw.Output()))
	assert.Equal(t, []int64{10, 11}, callOrder, "Handler calls out of order in serial mode")
}

func TestRPCServer_Run_EmptyBatch_Stream(t *testing.T) {
	t.Parallel()
	handler := &mockHandler{}
	input := `[]` + "\n"
	rw := newMockReadWriter([]byte(input))
	rp := NewStreamServerFromIO(rw, handler)

	err := runServerWithTimeout(t, rp, 100*time.Millisecond)
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	// Spec requires an error response for an empty batch
	expectedOutput := `{"jsonrpc":"2.0","error":{"code":-32600,"message":"Invalid Request"},"id":null}`
	assert.JSONEq(t, string(expectedOutput), string(rw.Output()))
}

func TestRPCServer_Run_InvalidJSON_Stream(t *testing.T) {
	t.Parallel()
	handler := &mockHandler{}
	input := `{"jsonrpc": "2.0", "method": "test", "id": 1]` // Missing closing brace
	rw := newMockReadWriter([]byte(input))
	rp := NewStreamServerFromIO(rw, handler)
	var decodeErr atomic.Value // Store decoding error

	rp.Callbacks.OnDecodingError = func(ctx context.Context, msg json.RawMessage, err error) {
		decodeErr.Store(err)
	}

	err := runServerWithTimeout(t, rp, 100*time.Millisecond)
	// The server might return io.EOF or context deadline depending on timing
	assert.True(t, errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF), "Expected EOF or DeadlineExceeded, got %v", err)

	// Expect a ParseError response
	expectedOutput := `{"jsonrpc":"2.0","error":{"code":-32700,"message":"Parse Error"},"id":null}`
	// Note: The default stream decoder might not write the error if the stream ends abruptly.
	// Let's check if the callback was hit instead.
	// assert.JSONEq(t, []byte(expectedOutput), rw.Output()) // This might fail
	assert.JSONEq(t, string(expectedOutput), string(rw.Output()))

	assert.NotNil(t, decodeErr.Load(), "OnDecodingError callback should have been called")
	decodeErrVal, ok := decodeErr.Load().(error)
	require.True(t, ok, "Stored decode error is not an error type")
	assert.ErrorContains(t, decodeErrVal, "unexpected end of JSON input")
}

func TestRPCServer_Run_InvalidRequest_Stream(t *testing.T) {
	t.Parallel()
	handler := &mockHandler{}
	// Invalid request: method is an integer, not a string
	input := `{"jsonrpc": "2.0", "method": 123, "id": 1}` + "\n"
	rw := newMockReadWriter([]byte(input))
	rp := NewStreamServerFromIO(rw, handler)
	var decodeErr atomic.Value

	rp.Callbacks.OnDecodingError = func(ctx context.Context, msg json.RawMessage, err error) {
		decodeErr.Store(err)
	}

	err := runServerWithTimeout(t, rp, 100*time.Millisecond)
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	// Expect an InvalidRequest response
	expectedOutput := `{"jsonrpc":"2.0","error":{"code":-32600,"message":"Invalid Request"},"id":null}`
	assert.JSONEq(t, string(expectedOutput), string(rw.Output()))
	assert.NotNil(t, decodeErr.Load(), "OnDecodingError callback should have been called")
	decodeErrVal, ok := decodeErr.Load().(error)
	require.True(t, ok, "Stored decode error is not an error type")
	assert.ErrorContains(t, decodeErrVal, "json: cannot unmarshal number into Go struct field Request.method of type string")
}

func TestRPCServer_Run_HandlerError_Stream(t *testing.T) {
	t.Parallel()
	handler := &mockHandler{
		handleFunc: func(ctx context.Context, req *Request) (any, error) {
			return nil, NewError(123, "handler error") // Custom error
		},
	}
	input := `{"jsonrpc": "2.0", "method": "errorMethod", "id": 2}` + "\n"
	rw := newMockReadWriter([]byte(input))
	rp := NewStreamServerFromIO(rw, handler)

	err := runServerWithTimeout(t, rp, 100*time.Millisecond)
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	expectedOutput := `{"jsonrpc":"2.0","error":{"code":123,"message":"handler error"},"id":2}`
	assert.JSONEq(t, string(expectedOutput), string(rw.Output()))
}

func TestRPCServer_Run_HandlerPanic_Stream(t *testing.T) {
	t.Parallel()
	handler := &mockHandler{}
	input := `{"jsonrpc": "2.0", "method": "panicMethod", "id": 3}` + "\n"
	rw := newMockReadWriter([]byte(input))
	rp := NewStreamServerFromIO(rw, handler)
	var panicInfo atomic.Value

	rp.Callbacks.OnHandlerPanic = func(ctx context.Context, req *Request, recovery any) {
		panicInfo.Store(recovery)
	}

	handler.TriggerPanic() // Make the handler panic on the next call

	err := runServerWithTimeout(t, rp, 100*time.Millisecond)
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	// Expect an InternalError response
	expectedOutput := `{"jsonrpc":"2.0","error":{"code":-32603,"message":"Internal error"},"id":3}`
	assert.JSONEq(t, string(expectedOutput), string(rw.Output()))
	assert.NotNil(t, panicInfo.Load(), "OnHandlerPanic callback should have been called")
	assert.Equal(t, "handler panic!", panicInfo.Load())
}

func TestRPCServer_Run_ContextCancel_Stream(t *testing.T) {
	t.Parallel()
	handler := &mockHandler{
		handleFunc: func(ctx context.Context, req *Request) (any, error) {
			// Simulate work that respects cancellation
			select {
			case <-time.After(200 * time.Millisecond):
				return "done", nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}
	input := `{"jsonrpc": "2.0", "method": "longRunning", "id": 4}` + "\n"
	rw := newMockReadWriter([]byte(input))
	rp := NewStreamServerFromIO(rw, handler)
	var exitErr atomic.Value

	rp.Callbacks.OnExit = func(ctx context.Context, err error) {
		exitErr.Store(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond) // Cancel before handler finishes
	defer cancel()

	runErr := rp.Run(ctx)

	// Expect context cancelled error from Run
	assert.ErrorIs(t, runErr, context.Canceled, "Run should return context.Canceled")

	// Check that OnExit was called with context.Canceled
	require.NotNil(t, exitErr.Load(), "OnExit callback not called")
	exitErrVal, ok := exitErr.Load().(error)
	require.True(t, ok, "Stored exit error is not an error type")
	assert.ErrorIs(t, exitErrVal, context.Canceled, "OnExit error mismatch")

	// No response should be sent if cancelled mid-request
	assert.Empty(t, rw.Output(), "No response should be sent after cancellation")
}

func TestRPCServer_Run_WaitOnClose(t *testing.T) {
	t.Parallel()
	var handlerFinished atomic.Bool
	handler := &mockHandler{
		handleFunc: func(ctx context.Context, req *Request) (any, error) {
			time.Sleep(50 * time.Millisecond) // Simulate work
			handlerFinished.Store(true)
			return "done", nil
		},
	}
	input := `{"jsonrpc": "2.0", "method": "waitTest", "id": 5}` + "\n"
	rw := newMockReadWriter([]byte(input))
	rp := NewStreamServerFromIO(rw, handler)
	rp.WaitOnClose = true

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond) // Cancel quickly
	defer cancel()

	runErr := rp.Run(ctx)

	// Expect context canceled because the overall run was canceled
	assert.ErrorIs(t, runErr, context.DeadlineExceeded)
	// Crucially, check that the handler *did* finish despite the early context cancel
	assert.True(t, handlerFinished.Load(), "Handler should have finished due to WaitOnClose")

	// Response should have been sent because WaitOnClose allowed it to finish
	expectedOutput := `{"jsonrpc":"2.0","result":"done","id":5}`
	assert.JSONEq(t, string(expectedOutput), string(rw.Output()))
}

func TestRPCServer_Run_NoRoutines(t *testing.T) {
	t.Parallel()
	var handlerGoroutineID, runGoroutineID int64
	handler := &mockHandler{
		handleFunc: func(ctx context.Context, req *Request) (any, error) {
			handlerGoroutineID = getGoroutineID() // Needs a helper to get goroutine ID, simplified here
			time.Sleep(10 * time.Millisecond)
			return "done", nil
		},
	}
	input := `{"jsonrpc": "2.0", "method": "noRoutineTest", "id": 6}` + "\n"
	rw := newMockReadWriter([]byte(input))
	rp := NewStreamServerFromIO(rw, handler)
	rp.NoRoutines = true

	var runErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		runGoroutineID = getGoroutineID()
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		runErr = rp.Run(ctx)
	}()

	wg.Wait()

	assert.ErrorIs(t, runErr, context.DeadlineExceeded)
	// This check is indicative; getting goroutine IDs reliably is complex.
	// The main point is that the handler blocks the Run loop.
	if handlerGoroutineID != 0 && runGoroutineID != 0 {
		assert.Equal(t, runGoroutineID, handlerGoroutineID, "Handler should run on the same goroutine as Run when NoRoutines=true")
	}

	expectedOutput := `{"jsonrpc":"2.0","result":"done","id":6}`
	assert.JSONEq(t, string(expectedOutput), string(rw.Output()))
}

// Helper to get goroutine ID (platform dependent, simplified example)
func getGoroutineID() int64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	// Expected format: "goroutine GID [status]:"
	fields := bytes.Fields(buf[:n])
	if len(fields) < 2 {
		return 0 // Could not parse
	}
	idField := fields[1]
	id, _ := strconv.ParseInt(string(idField), 10, 64)
	return id
}

// Note: getGoroutineID requires these imports, which might conflict if placed at top level
// import "runtime"
// import "strconv"

func TestRPCServer_Close(t *testing.T) {
	t.Parallel()
	handler := &mockHandler{} // Define handler locally for this test
	rw := newMockReadWriter(nil)
	rp := NewStreamServerFromIO(rw, handler)

	// Run the server briefly to ensure components are active
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	_ = rp.Run(ctx)
	cancel() // Ensure Run exits

	err := rp.Close()
	assert.ErrorContains(t, err, "already closed")

	// Verify underlying closer (mockReadWriter) was called
	assert.True(t, rw.closed, "Underlying ReadWriter should be closed")

	// Test double close
	err = rp.Close()
	assert.Error(t, err, "Double close should return an error") // mockReadWriter returns error on double close
}

func TestRPCServer_Callbacks(t *testing.T) {
	t.Parallel()
	var onExitCalled, onDecodeErrCalled, onEncodeErrCalled, onPanicCalled atomic.Bool

	handler := &mockHandler{
		handleFunc: func(ctx context.Context, req *Request) (any, error) {
			if req.Method == "makeError" {
				return nil, errors.New("handler-error") // Not an RPCError, will be wrapped
			}
			if req.Method == "makePanic" {
				panic("handler-panic")
			}
			return "ok", nil
		},
	}
	// Use packet conn to easily simulate encoding errors
	conn := newMockPacketConn()
	// Force encoding error
	conn.writeErr = errors.New("forced encoding error")

	rp := NewRPCServerFromPacket(conn, handler)

	rp.Callbacks.OnExit = func(ctx context.Context, err error) {
		onExitCalled.Store(true)
		// Check if the error contains context.Canceled or DeadlineExceeded
		assert.True(t, errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded), "OnExit should receive context cancel/deadline error, got: %v", err)
	}
	rp.Callbacks.OnDecodingError = func(ctx context.Context, msg json.RawMessage, err error) {
		onDecodeErrCalled.Store(true)
		assert.Error(t, err)
	}
	rp.Callbacks.OnEncodingError = func(ctx context.Context, data any, err error) {
		onEncodeErrCalled.Store(true)
		assert.ErrorContains(t, err, "forced encoding error")
		// Check data type (should be *Response for request, []any for batch)
		_, isResponse := data.(*Response)
		_, isSlice := data.([]any)
		assert.True(t, isResponse || isSlice, "Unexpected data type in OnEncodingError: %T", data)

	}
	rp.Callbacks.OnHandlerPanic = func(ctx context.Context, req *Request, recovery any) {
		onPanicCalled.Store(true)
		assert.Equal(t, "handler-panic", recovery)
		assert.Equal(t, "makePanic", req.Method)
	}

	// 1. Trigger Decode Error
	conn.SendData([]byte(`{invalid json`))
	// 2. Trigger Encode Error (via normal request)
	conn.SendData([]byte(`{"jsonrpc": "2.0", "method": "normal", "id": 101}`))
	// 3. Trigger Panic
	conn.SendData([]byte(`{"jsonrpc": "2.0", "method": "makePanic", "id": 102}`))
	// 4. Trigger Encode Error (via handler error response) - write error still set
	conn.SendData([]byte(`{"jsonrpc": "2.0", "method": "makeError", "id": 103}`))
	// 5. Trigger Encode Error (via batch response) - write error still set
	conn.SendData([]byte(`[{"jsonrpc": "2.0", "method": "batchA", "id": 104}, {"jsonrpc": "2.0", "method": "batchB", "id": 105}]`))

	// Run server briefly and cancel
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	runErr := rp.Run(ctx)

	// Run might return DeadlineExceeded or Canceled depending on timing
	assert.True(t, errors.Is(runErr, context.DeadlineExceeded) || errors.Is(runErr, context.Canceled), "Run error mismatch: %v", runErr)

	// Check callbacks were called
	assert.True(t, onDecodeErrCalled.Load(), "OnDecodingError callback was not called")
	assert.True(t, onEncodeErrCalled.Load(), "OnEncodingError callback was not called")
	assert.True(t, onPanicCalled.Load(), "OnHandlerPanic callback was not called")
	assert.True(t, onExitCalled.Load(), "OnExit callback was not called")
}

func TestRPCServer_ContextValues(t *testing.T) {
	t.Parallel()
	var seenServer *RPCServer
	var seenAddr net.Addr
	var mu sync.Mutex // Protect access to seenServer/seenAddr

	handler := &mockHandler{
		handleFunc: func(ctx context.Context, req *Request) (any, error) {
			mu.Lock()
			// Check CtxRPCServer
			srvVal := ctx.Value(CtxRPCServer)
			require.NotNil(t, srvVal, "CtxRPCServer not found in context")
			srv, ok := srvVal.(*RPCServer)
			require.True(t, ok, "CtxRPCServer has wrong type")
			seenServer = srv

			// Check CtxFromAddr (only for packet server)
			addrVal := ctx.Value(CtxFromAddr)
			if addrVal != nil { // Will be nil for stream server
				addr, ok := addrVal.(net.Addr)
				require.True(t, ok, "CtxFromAddr has wrong type")
				seenAddr = addr
			}
			mu.Unlock()
			return "context ok", nil
		},
	}

	// Test with Packet Server
	conn := newMockPacketConn()
	rpPacket := NewRPCServerFromPacket(conn, handler)
	inputPacket := `{"jsonrpc": "2.0", "method": "ctxTestPacket", "id": 201}`
	conn.SendData([]byte(inputPacket))

	errPacket := runServerWithTimeout(t, rpPacket, 100*time.Millisecond)
	assert.ErrorIs(t, errPacket, context.DeadlineExceeded)

	mu.Lock()
	assert.Same(t, rpPacket, seenServer, "CtxRPCServer mismatch for packet server")
	require.NotNil(t, seenAddr, "CtxFromAddr should not be nil for packet server")
	assert.Equal(t, conn.remoteAddr.String(), seenAddr.String(), "CtxFromAddr mismatch for packet server")
	// Reset for next test
	seenServer = nil
	seenAddr = nil
	mu.Unlock()

	// Test with Stream Server
	rw := newMockReadWriter([]byte(`{"jsonrpc": "2.0", "method": "ctxTestStream", "id": 202}` + "\n"))
	rpStream := NewStreamServerFromIO(rw, handler)
	errStream := runServerWithTimeout(t, rpStream, 100*time.Millisecond)
	assert.ErrorIs(t, errStream, context.DeadlineExceeded)

	mu.Lock()
	assert.Same(t, rpStream, seenServer, "CtxRPCServer mismatch for stream server")
	assert.Nil(t, seenAddr, "CtxFromAddr should be nil for stream server")
	mu.Unlock()
}
