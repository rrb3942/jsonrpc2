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

	"os"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Test Helpers ---

// setupTestStreamServer creates an RPCServer connected via pipes using mockConn.
// clientToServerWriter is the writer the test uses to send requests to the server.
// serverOutputBuf captures the server's output written via the mockConn.
// conn is the mock connection wrapping the pipes from the server's perspective.
// rp is the configured RPCServer.
func setupTestStreamServer(handler Handler) (clientToServerWriter io.WriteCloser, serverOutputBuf *bytes.Buffer, conn *mockConn, rp *RPCServer) {
	clientToServerReader, clientToServerWriterPipe := io.Pipe() // Client writes requests here
	serverToClientReader, serverToClientWriterPipe := io.Pipe() // Server writes responses here
	serverOutputBuf = new(bytes.Buffer)

	// The mockConn simulates the server's perspective:
	// - Reads from clientToServerReader (what the client writes)
	// - Writes to serverToClientWriterPipe (what the client reads)
	// - We also tee the writes to serverOutputBuf for inspection
	conn = &mockConn{
		r: clientToServerReader,                                     // Server reads what client writes
		w: io.MultiWriter(serverToClientWriterPipe, serverOutputBuf), // Server writes to client pipe and buffer
		c: serverToClientWriterPipe,                                  // Closing conn closes the server writer pipe
	}

	// The client reads from serverToClientReader
	_ = serverToClientReader // Assign to blank identifier if client reading isn't needed directly in helper

	rp = NewStreamServerFromIO(conn, handler)

	// Return the writer the client uses to send data, the buffer capturing server output, the server's mockConn, and the server
	return clientToServerWriterPipe, serverOutputBuf, conn, rp
}

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
	// Use the setup helper to get a valid server instance with mockConn
	clientWriter, _, conn, rp := setupTestStreamServer(handler)
	defer conn.Close()
	defer clientWriter.Close()

	// The test primarily checks the fields of the created RPCServer
	require.NotNil(t, rp)
	assert.Equal(t, handler, rp.Handler)
	// Check that the internal decoder/encoder are shims wrapping the mockConn
	_, decOk := rp.decoder.(*decoderShim)
	assert.True(t, decOk, "Internal decoder should be a shim")
	_, encOk := rp.encoder.(*encoderShim)
	assert.True(t, encOk, "Internal encoder should be a shim")
	assert.NotNil(t, rp.encoder)
	assert.False(t, rp.SerialBatch)
	assert.False(t, rp.NoRoutines)
	assert.False(t, rp.WaitOnClose)
	assert.NotNil(t, rp.Callbacks.OnHandlerPanic) // Default should be set
}

func TestNewStreamServerFromIO(t *testing.T) {
	t.Parallel()
	handler := &mockHandler{}
	clientWriter, _, conn, rp := setupTestStreamServer(handler) // Use helper
	defer conn.Close()                                          // Close the mockConn
	defer clientWriter.Close()                                  // Close the client writer pipe

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
	clientWriter, serverOutput, conn, rp := setupTestStreamServer(handler)
	defer conn.Close()

	input := `{"jsonrpc": "2.0", "method": "testMethod", "id": 1}` + "\n"

	// Write input to the server via the client's writer pipe
	go func() {
		_, errWrite := clientWriter.Write([]byte(input))
		require.NoError(t, errWrite)
		// Close the writer to signal EOF to the server after sending the request
		errClose := clientWriter.Close()
		require.NoError(t, errClose)
	}()

	// Run the server; it should process the request and exit upon EOF
	err := rp.Run(context.Background()) // Run without timeout, expect EOF exit
	assert.ErrorIs(t, err, io.EOF, "Server should exit with io.EOF after processing single request")

	expectedOutput := `{"jsonrpc":"2.0","result":"handled testMethod","id":1}`
	assert.JSONEq(t, expectedOutput, serverOutput.String())
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
	clientWriter, serverOutput, conn, rp := setupTestStreamServer(handler)
	defer conn.Close()

	// Notification has no ID
	input := `{"jsonrpc": "2.0", "method": "notifyMethod"}` + "\n"

	go func() {
		_, errWrite := clientWriter.Write([]byte(input))
		require.NoError(t, errWrite)
		// Close the writer to signal EOF
		errClose := clientWriter.Close()
		require.NoError(t, errClose)
	}()

	// Run the server; it should process the notification and exit upon EOF
	err := rp.Run(context.Background())
	assert.ErrorIs(t, err, io.EOF, "Server should exit with io.EOF after processing notification")

	// No response should be written for a notification
	assert.Empty(t, serverOutput.String(), "Should not write a response for a notification")
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
	clientWriter, serverOutput, conn, rp := setupTestStreamServer(handler)
	defer conn.Close()
	rp.SerialBatch = false // Explicitly parallel (default)

	go func() {
		_, errWrite := clientWriter.Write([]byte(input))
		require.NoError(t, errWrite)
		errClose := clientWriter.Close()
		require.NoError(t, errClose)
	}()

	startTime := time.Now()
	// Run until EOF
	err := rp.Run(context.Background())
	duration := time.Since(startTime)

	assert.ErrorIs(t, err, io.EOF)
	// Check if it ran somewhat concurrently (less than 2*10ms + overhead)
	assert.Less(t, duration, 40*time.Millisecond, "Batch processing took too long, likely serial") // Increased threshold slightly for pipe overhead

	// Note: Order in response batch is not guaranteed by spec unless requests are processed serially.
	// We need to unmarshal and check presence, not exact string match.
	var responses []Response
	err = json.Unmarshal(serverOutput.Bytes(), &responses)
	require.NoError(t, err, "Failed to unmarshal batch response: %s", serverOutput.String())
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
	clientWriter, serverOutput, conn, rp := setupTestStreamServer(handler)
	defer conn.Close()
	rp.SerialBatch = true // Force serial execution

	go func() {
		_, errWrite := clientWriter.Write([]byte(input))
		require.NoError(t, errWrite)
		errClose := clientWriter.Close()
		require.NoError(t, errClose)
	}()

	startTime := time.Now()
	// Run until EOF
	err := rp.Run(context.Background())
	duration := time.Since(startTime)

	assert.ErrorIs(t, err, io.EOF)
	// Check if it ran serially (at least 2*10ms + overhead)
	assert.GreaterOrEqual(t, duration, 20*time.Millisecond, "Batch processing finished too quickly, likely parallel")

	// Order must be preserved in serial execution
	expectedOutput := `[
		{"jsonrpc":"2.0","result":"handled batch1 with id 10","id":10},
		{"jsonrpc":"2.0","result":"handled batch2 with id 11","id":11}
	]`
	assert.JSONEq(t, expectedOutput, serverOutput.String())
	assert.Equal(t, []int64{10, 11}, callOrder, "Handler calls out of order in serial mode")
}

func TestRPCServer_Run_EmptyBatch_Stream(t *testing.T) {
	t.Parallel()
	handler := &mockHandler{}
	clientWriter, serverOutput, conn, rp := setupTestStreamServer(handler)
	defer conn.Close()

	input := `[]` + "\n"

	go func() {
		_, errWrite := clientWriter.Write([]byte(input))
		require.NoError(t, errWrite)
		errClose := clientWriter.Close()
		require.NoError(t, errClose)
	}()

	// Run until EOF
	err := rp.Run(context.Background())
	assert.ErrorIs(t, err, io.EOF)

	// Spec requires an error response for an empty batch
	expectedOutput := `{"jsonrpc":"2.0","error":{"code":-32600,"message":"Invalid Request"},"id":null}`
	assert.JSONEq(t, expectedOutput, serverOutput.String())
}

func TestRPCServer_Run_InvalidJSON_Stream(t *testing.T) {
	t.Parallel()
	handler := &mockHandler{}
	clientWriter, serverOutput, conn, rp := setupTestStreamServer(handler)
	defer conn.Close()

	input := `{"jsonrpc": "2.0", "method": "test", "id": 1]` // Missing closing brace
	var decodeErr atomic.Value                              // Store decoding error

	rp.Callbacks.OnDecodingError = func(ctx context.Context, msg json.RawMessage, err error) {
		decodeErr.Store(err)
	}

	go func() {
		_, errWrite := clientWriter.Write([]byte(input))
		require.NoError(t, errWrite)
		// Don't close the pipe immediately, let the server try to decode the invalid JSON
		// Closing might cause EOF before the parse error is handled.
		// Let Run exit due to the decoding error propagation.
		// errClose := clientWriter.Close() // Don't close here
		// require.NoError(t, errClose)
	}()

	// Run the server; it should detect the error and exit
	err := rp.Run(context.Background())
	// Expect the underlying JSON parsing error
	require.Error(t, err)
	assert.ErrorContains(t, err, "unexpected end of JSON input", "Run should return the JSON parsing error")

	// Expect a ParseError response to have been written before the exit
	expectedOutput := `{"jsonrpc":"2.0","error":{"code":-32700,"message":"Parse Error"},"id":null}`
	assert.JSONEq(t, expectedOutput, serverOutput.String(), "Server should write Parse Error response")

	// Callback should *not* have been called because the error is returned directly by DecodeFrom in Run loop
	assert.Nil(t, decodeErr.Load(), "OnDecodingError callback should NOT have been called for direct DecodeFrom error")
}

func TestRPCServer_Run_InvalidRequest_Stream(t *testing.T) {
	t.Parallel()
	handler := &mockHandler{}
	clientWriter, serverOutput, conn, rp := setupTestStreamServer(handler)
	defer conn.Close()

	// Invalid request: method is an integer, not a string
	input := `{"jsonrpc": "2.0", "method": 123, "id": 1}` + "\n"
	var decodeErr atomic.Value

	rp.Callbacks.OnDecodingError = func(ctx context.Context, msg json.RawMessage, err error) {
		decodeErr.Store(err)
	}

	go func() {
		_, errWrite := clientWriter.Write([]byte(input))
		require.NoError(t, errWrite)
		// Don't close pipe immediately, let server process the invalid request structure
		// errClose := clientWriter.Close() // Don't close here
		// require.NoError(t, errClose)
	}()

	// Run the server; it should detect the error and exit
	err := rp.Run(context.Background())
	// Expect the underlying JSON unmarshal type error
	require.Error(t, err)
	assert.ErrorContains(t, err, "json: cannot unmarshal number into Go struct field Request.method of type string", "Run should return the JSON type error")

	// Expect an InvalidRequest response to have been written
	expectedOutput := `{"jsonrpc":"2.0","error":{"code":-32600,"message":"Invalid Request"},"id":null}`
	assert.JSONEq(t, expectedOutput, serverOutput.String(), "Server should write Invalid Request response")

	// Callback should *not* have been called because the error is returned by DecodeFrom
	assert.Nil(t, decodeErr.Load(), "OnDecodingError callback should NOT have been called for direct DecodeFrom error")
}

func TestRPCServer_Run_HandlerError_Stream(t *testing.T) {
	t.Parallel()
	handler := &mockHandler{
		handleFunc: func(ctx context.Context, req *Request) (any, error) {
			return nil, NewError(123, "handler error") // Custom error
		},
	}
	clientWriter, serverOutput, conn, rp := setupTestStreamServer(handler)
	defer conn.Close()

	input := `{"jsonrpc": "2.0", "method": "errorMethod", "id": 2}` + "\n"

	go func() {
		_, errWrite := clientWriter.Write([]byte(input))
		require.NoError(t, errWrite)
		errClose := clientWriter.Close()
		require.NoError(t, errClose)
	}()

	err := rp.Run(context.Background())
	assert.ErrorIs(t, err, io.EOF)

	expectedOutput := `{"jsonrpc":"2.0","error":{"code":123,"message":"handler error"},"id":2}`
	assert.JSONEq(t, expectedOutput, serverOutput.String())
}

func TestRPCServer_Run_HandlerPanic_Stream(t *testing.T) {
	t.Parallel()
	handler := &mockHandler{}
	clientWriter, serverOutput, conn, rp := setupTestStreamServer(handler)
	defer conn.Close()

	input := `{"jsonrpc": "2.0", "method": "panicMethod", "id": 3}` + "\n"
	var panicInfo atomic.Value

	rp.Callbacks.OnHandlerPanic = func(ctx context.Context, req *Request, recovery any) {
		panicInfo.Store(recovery)
	}

	handler.TriggerPanic() // Make the handler panic on the next call

	go func() {
		_, errWrite := clientWriter.Write([]byte(input))
		require.NoError(t, errWrite)
		errClose := clientWriter.Close()
		require.NoError(t, errClose)
	}()

	err := rp.Run(context.Background())
	assert.ErrorIs(t, err, io.EOF)

	// Expect an InternalError response
	expectedOutput := `{"jsonrpc":"2.0","error":{"code":-32603,"message":"Internal error"},"id":3}`
	assert.JSONEq(t, expectedOutput, serverOutput.String())
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
	clientWriter, serverOutput, conn, rp := setupTestStreamServer(handler)
	defer conn.Close()

	input := `{"jsonrpc": "2.0", "method": "longRunning", "id": 4}` + "\n"
	var exitErr atomic.Value

	rp.Callbacks.OnExit = func(ctx context.Context, err error) {
		exitErr.Store(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond) // Cancel before handler finishes
	defer cancel()

	// Write the request in a goroutine
	go func() {
		_, errWrite := clientWriter.Write([]byte(input))
		// Don't check error strictly, pipe might break due to cancellation
		if errWrite == nil {
			// Don't close the writer here, let the cancellation break the read/write
			// errClose := clientWriter.Close()
			// require.NoError(t, errClose)
		}
	}()

	runErr := rp.Run(ctx)

	// Expect context cancelled error from Run (or potentially a pipe error caused by cancellation)
	assert.True(t, errors.Is(runErr, context.Canceled) || errors.Is(runErr, os.ErrClosed) || errors.Is(runErr, io.ErrClosedPipe), "Run should return context.Canceled or pipe error, got: %v", runErr)

	// Check that OnExit was called with context.Canceled
	require.NotNil(t, exitErr.Load(), "OnExit callback not called")
	exitErrVal, ok := exitErr.Load().(error)
	require.True(t, ok, "Stored exit error is not an error type")
	// The error passed to OnExit should reflect the context cancellation
	assert.ErrorIs(t, exitErrVal, context.Canceled, "OnExit error mismatch")

	// No response should be sent if cancelled mid-request
	assert.Empty(t, serverOutput.String(), "No response should be sent after cancellation")
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
	clientWriter, serverOutput, conn, rp := setupTestStreamServer(handler)
	defer conn.Close()
	rp.WaitOnClose = true

	input := `{"jsonrpc": "2.0", "method": "waitTest", "id": 5}` + "\n"

	go func() {
		_, errWrite := clientWriter.Write([]byte(input))
		require.NoError(t, errWrite)
		// Don't close the writer, let Run exit via context cancellation
		// errClose := clientWriter.Close()
		// require.NoError(t, errClose)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond) // Cancel quickly
	defer cancel()

	runErr := rp.Run(ctx)

	// Expect context canceled/deadline exceeded because the overall run was canceled
	assert.True(t, errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded), "Run error mismatch: %v", runErr)
	// Crucially, check that the handler *did* finish despite the early context cancel
	assert.True(t, handlerFinished.Load(), "Handler should have finished due to WaitOnClose")

	// Response should have been sent because WaitOnClose allowed it to finish
	expectedOutput := `{"jsonrpc":"2.0","result":"done","id":5}`
	assert.JSONEq(t, expectedOutput, serverOutput.String())
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
	clientWriter, serverOutput, conn, rp := setupTestStreamServer(handler)
	defer conn.Close()
	rp.NoRoutines = true

	input := `{"jsonrpc": "2.0", "method": "noRoutineTest", "id": 6}` + "\n"

	go func() {
		_, errWrite := clientWriter.Write([]byte(input))
		require.NoError(t, errWrite)
		errClose := clientWriter.Close()
		require.NoError(t, errClose)
	}()

	runGoroutineID = getGoroutineID()
	// Run until EOF
	runErr := rp.Run(context.Background())

	assert.ErrorIs(t, runErr, io.EOF)
	// This check is indicative; getting goroutine IDs reliably is complex.
	// The main point is that the handler blocks the Run loop.
	if handlerGoroutineID != 0 && runGoroutineID != 0 {
		assert.Equal(t, runGoroutineID, handlerGoroutineID, "Handler should run on the same goroutine as Run when NoRoutines=true")
	}

	expectedOutput := `{"jsonrpc":"2.0","result":"done","id":6}`
	assert.JSONEq(t, expectedOutput, serverOutput.String())
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
	handler := &mockHandler{}
	clientWriter, _, conn, rp := setupTestStreamServer(handler) // Use helper
	defer clientWriter.Close()                                  // Close client writer pipe

	// Run the server briefly to ensure components are active, then cancel
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	_ = rp.Run(ctx) // Ignore error, likely deadline exceeded
	cancel()

	// Close the server
	err := rp.Close()
	assert.NoError(t, err, "First rp.Close() should succeed")

	// Verify underlying mockConn closer was called (mockConn.Close() returns conn.closeErr)
	// We didn't set conn.closeErr, so it should be nil.

	// Test double closing the RPCServer (should be idempotent or handle underlying errors)
	err = rp.Close()
	// mockConn.Close() doesn't inherently return error on double close,
	// but the underlying pipe closers might. Let's just check it doesn't panic.
	// It might return io.ErrClosedPipe if the underlying pipe was already closed.
	if err != nil {
		assert.ErrorIs(t, err, io.ErrClosedPipe, "Second rp.Close() should be no-op or return pipe closed error")
	}

	// Test closing with underlying error
	expectedErr := errors.New("mock close error")
	clientWriterErr, _, connWithErr, rpWithErr := setupTestStreamServer(handler)
	defer clientWriterErr.Close()
	connWithErr.closeErr = expectedErr // Set error on the mockConn

	err = rpWithErr.Close()
	assert.ErrorIs(t, err, expectedErr, "rp.Close() did not return expected underlying error")
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
	packetConn := newMockPacketConn() // Renamed variable to avoid confusion
	// Force encoding error
	packetConn.writeErr = errors.New("forced encoding error")

	rp := NewRPCServerFromPacket(packetConn, handler)

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
	packetConn.SendData([]byte(`{invalid json`))
	// 2. Trigger Encode Error (via normal request)
	packetConn.SendData([]byte(`{"jsonrpc": "2.0", "method": "normal", "id": 101}`))
	// 3. Trigger Panic
	packetConn.SendData([]byte(`{"jsonrpc": "2.0", "method": "makePanic", "id": 102}`))
	// 4. Trigger Encode Error (via handler error response) - write error still set
	packetConn.SendData([]byte(`{"jsonrpc": "2.0", "method": "makeError", "id": 103}`))
	// 5. Trigger Encode Error (via batch response) - write error still set
	packetConn.SendData([]byte(`[{"jsonrpc": "2.0", "method": "batchA", "id": 104}, {"jsonrpc": "2.0", "method": "batchB", "id": 105}]`))

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
	clientWriterStream, _, connStream, rpStream := setupTestStreamServer(handler)
	defer connStream.Close()
	inputSteam := `{"jsonrpc": "2.0", "method": "ctxTestStream", "id": 202}` + "\n"

	go func() {
		_, errWrite := clientWriterStream.Write([]byte(inputSteam))
		require.NoError(t, errWrite)
		errClose := clientWriterStream.Close()
		require.NoError(t, errClose)
	}()

	errStream := rpStream.Run(context.Background())
	assert.ErrorIs(t, errStream, io.EOF)

	mu.Lock()
	assert.Same(t, rpStream, seenServer, "CtxRPCServer mismatch for stream server")
	assert.Nil(t, seenAddr, "CtxFromAddr should be nil for stream server")
	mu.Unlock()
}
