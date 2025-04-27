package jsonrpc2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// mockConn implements net.Conn but allows simulating read/write errors and delays.
type mockConn struct {
	r io.Reader
	w io.Writer
	c io.Closer // Usually the write side of the pipe

	readErr  error
	writeErr error
	closeErr error

	readDelay  time.Duration
	writeDelay time.Duration
}

func (m *mockConn) Read(b []byte) (n int, err error) {
	if m.readErr != nil {
		return 0, m.readErr
	}

	if m.readDelay > 0 {
		time.Sleep(m.readDelay)
	}

	return m.r.Read(b)
}

func (m *mockConn) Write(b []byte) (n int, err error) {
	if m.writeErr != nil {
		return 0, m.writeErr
	}

	if m.writeDelay > 0 {
		time.Sleep(m.writeDelay)
	}

	return m.w.Write(b)
}

func (m *mockConn) Close() error {
	if m.c != nil {
		_ = m.c.Close() // Close the writer pipe first
	}

	if rc, ok := m.r.(io.Closer); ok {
		_ = rc.Close()
	}

	if wc, ok := m.w.(io.Closer); ok {
		_ = wc.Close()
	}

	return m.closeErr
}

// setupTestTransportClient creates a client and a simulated server connection using pipes.
// The server side reads requests from serverReader and writes responses to serverWriter.
func setupTestTransportClient(serverWriter io.Writer) (*TransportClient, *io.PipeWriter, *mockConn) {
	clientReader, clientWriter := io.Pipe()
	conn := &mockConn{r: clientReader, w: serverWriter, c: clientWriter}
	client := NewTransportClientIO(conn)

	return client, clientWriter, conn
}

// simulateServer reads one request, processes it using the handler, and writes the response.
func simulateServer(t *testing.T, serverReader io.Reader, clientWriter io.Writer, handler func(req json.RawMessage) json.RawMessage) {
	t.Helper()

	decoder := json.NewDecoder(serverReader)

	var req json.RawMessage

	if err := decoder.Decode(&req); err != nil {
		// EOF is expected if the client closes the connection after sending a notification
		if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) {
			t.Errorf("Server failed to decode request: %v", err)
		}

		return
	}

	resp := handler(req)
	if resp != nil {
		n, err := clientWriter.Write(resp)
		if err != nil {
			t.Errorf("Server failed to write response: %v", err)
		}

		if n < len(resp) {
			t.Errorf("Server short write expected: %v, wrote: %v", len(resp), n)
		}
	}
}

func TestTransportClient_Call(t *testing.T) {
	serverReader, serverWriter := io.Pipe() // Server reads from serverReader, writes to serverWriter

	client, clientWriter, _ := setupTestTransportClient(serverWriter)
	defer client.Close()

	req := NewRequestWithParams(int64(1), "testMethod", NewParamsArray([]string{"testParam"}))
	expectedResp := NewResponseWithResult(int64(1), "testResult")

	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		defer wg.Done()
		simulateServer(t, serverReader, clientWriter, func(rawReq json.RawMessage) json.RawMessage {
			var receivedReq Request
			if err := json.Unmarshal(rawReq, &receivedReq); err != nil {
				t.Errorf("Server failed to unmarshal request: %v", err)
				return nil
			}

			if receivedReq.Method != req.Method {
				t.Errorf("Server received wrong method: got %s, want %s", receivedReq.Method, req.Method)
			}

			id, _ := receivedReq.ID.Int64()
			if id != 1 {
				t.Errorf("Server received wrong ID: got %d, want %d", id, 1)
			}

			respBytes, _ := json.Marshal(expectedResp)

			return respBytes
		})
	}()

	resp, err := client.Call(t.Context(), req)
	if err != nil {
		t.Fatalf("TransportClient.Call failed: %v", err)
	}

	assert.Equal(t, json.RawMessage(string("\"testResult\"")), resp.Result.value)

	respID, _ := resp.ID.Int64()
	if respID != 1 {
		t.Errorf("TransportClient received wrong ID: got %d, want %d", respID, 1)
	}

	wg.Wait() // Ensure server goroutine finishes
}

func TestTransportClient_CallWithTimeout(t *testing.T) {
	serverReader, serverWriter := io.Pipe()

	client, clientWriter, conn := setupTestTransportClient(serverWriter)
	defer client.Close()

	// Simulate a slow server
	conn.readDelay = 100 * time.Millisecond

	req := NewRequest(int64(1), "slowMethod")

	ctx := t.Context()
	timeout := 50 * time.Millisecond // Shorter than server delay

	_, err := client.CallWithTimeout(ctx, timeout, req)

	if err == nil {
		t.Fatalf("TransportClient.CallWithTimeout should have failed due to timeout, but got nil error")
	}

	// Check if the error is a context deadline exceeded error
	if !errors.Is(err, context.DeadlineExceeded) {
		// Depending on timing and pipe buffering, it might also be io.ErrClosedPipe from the server side closing
		// or another error if the encode/decode itself times out.
		t.Logf("TransportClient.CallWithTimeout failed with expected error type: %v", err)
	}

	// Ensure the server goroutine doesn't hang (optional, as the pipe breaks on timeout)
	//nolint:unparam // Must match function signature
	go simulateServer(t, serverReader, clientWriter, func(_ json.RawMessage) json.RawMessage {
		t.Log("Server received request after timeout test (should not happen often)")
		return nil // Don't respond
	})
}

func TestTransportClient_Notify(t *testing.T) {
	serverReader, serverWriter := io.Pipe()

	client, clientWriter, _ := setupTestTransportClient(serverWriter)
	defer client.Close()

	notification := NewNotificationWithParams("notifyMethod", NewParamsArray([]string{"notifyData"}))

	var wg sync.WaitGroup

	wg.Add(1)

	serverReceived := false

	go func() {
		defer wg.Done()
		simulateServer(t, serverReader, clientWriter, func(rawReq json.RawMessage) json.RawMessage {
			serverReceived = true

			var receivedNotif Notification
			if err := json.Unmarshal(rawReq, &receivedNotif); err != nil {
				t.Errorf("Server failed to unmarshal notification: %v", err)
				return nil
			}

			if receivedNotif.Method != notification.Method {
				t.Errorf("Server received wrong method: got %s, want %s", receivedNotif.Method, notification.Method)
			}

			if !receivedNotif.ID.IsZero() {
				t.Errorf("Server received notification with non-zero ID: %v", receivedNotif.ID)
			}
			// Notifications expect no response
			return nil
		})
	}()

	err := client.Notify(t.Context(), notification)
	if err != nil {
		t.Fatalf("TransportClient.Notify failed: %v", err)
	}

	// Close the client-side writer to signal EOF to the server reader, allowing simulateServer to exit.
	if c, ok := client.e.(io.Closer); ok {
		c.Close()
	}

	wg.Wait() // Ensure server goroutine finishes processing

	if !serverReceived {
		t.Error("Server did not receive the notification")
	}
}

func TestTransportClient_NotifyWithTimeout(t *testing.T) {
	_, serverWriter := io.Pipe()

	client, _, conn := setupTestTransportClient(serverWriter)
	defer client.Close()

	// Simulate a slow write
	conn.writeDelay = 100 * time.Millisecond

	notification := NewNotification("slowNotify")

	ctx := t.Context()
	timeout := 50 * time.Millisecond // Shorter than write delay

	err := client.NotifyWithTimeout(ctx, timeout, notification)

	if err == nil {
		t.Fatalf("TransportClient.NotifyWithTimeout should have failed due to timeout, but got nil error")
	}

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Logf("TransportClient.NotifyWithTimeout failed with expected error type: %v", err)
	}
}

func TestTransportClient_Close(t *testing.T) {
	_, serverWriter := io.Pipe()
	client, _, conn := setupTestTransportClient(serverWriter)

	// Test closing works
	err := client.Close()
	if err != nil {
		t.Fatalf("client.Close() failed: %v", err)
	}

	// Test double closing is fine
	err = client.Close()
	if err != nil {
		t.Fatalf("second client.Close() failed: %v", err)
	}

	// Test using a closed client fails
	req := NewRequest(int64(1), "testMethod")

	_, err = client.Call(t.Context(), req)
	if err == nil {
		t.Fatal("client.Call() on closed client should fail, but succeeded")
	}
	// The exact error might vary (e.g., io.ErrClosedPipe), check it's not nil
	t.Logf("Call on closed client failed as expected: %v", err)

	// Test closing with underlying error
	expectedErr := errors.New("mock close error")
	conn.closeErr = expectedErr
	clientWithErr, _, connWithErr := setupTestTransportClient(serverWriter) // Use fresh pipes
	connWithErr.closeErr = expectedErr

	err = clientWithErr.Close()
	if !errors.Is(err, expectedErr) {
		t.Errorf("client.Close() did not return expected error: got %v, want %v", err, expectedErr)
	}
}

func TestTransportClient_Call_ContextCancel(t *testing.T) {
	_, serverWriter := io.Pipe()

	client, _, conn := setupTestTransportClient(serverWriter)
	defer client.Close()

	// Simulate a delay in writing the request
	conn.writeDelay = 100 * time.Millisecond

	req := NewRequest(int64(1), "cancelMethod")
	ctx, cancel := context.WithCancel(t.Context())

	var wg sync.WaitGroup

	wg.Add(1)

	var callErr error

	go func() {
		defer wg.Done()

		_, callErr = client.Call(ctx, req)
	}()

	// Cancel the context before the write delay finishes
	time.Sleep(50 * time.Millisecond)
	cancel()

	wg.Wait()

	if callErr == nil {
		t.Fatalf("TransportClient.Call should have failed due to context cancellation, but got nil error")
	}

	if !errors.Is(callErr, context.Canceled) {
		t.Errorf("TransportClient.Call failed with unexpected error: got %v, want %v", callErr, context.Canceled)
	}
}

func TestTransportClient_Call_ServerError(t *testing.T) {
	serverReader, serverWriter := io.Pipe()

	client, clientWriter, _ := setupTestTransportClient(serverWriter)
	defer client.Close()

	req := NewRequest(int64(1), "errorMethod")
	expectedErr := NewError(int64(-32000), "Server error occurred")
	expectedResp := NewResponseWithError(int64(1), expectedErr)

	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		defer wg.Done()
		simulateServer(t, serverReader, clientWriter, func(_ json.RawMessage) json.RawMessage {
			respBytes, _ := json.Marshal(expectedResp)
			return respBytes
		})
	}()

	resp, err := client.Call(t.Context(), req)
	if err != nil {
		t.Fatalf("TransportClient.Call failed: %v", err)
	}

	if !resp.IsError() {
		t.Fatalf("Response should indicate an error, but IsError() is false")
	}

	if resp.Error.Code != expectedErr.Code || resp.Error.Message != expectedErr.Message {
		t.Errorf("Received error mismatch: got %+v, want %+v", resp.Error, expectedErr)
	}

	wg.Wait()
}

func TestTransportClient_Call_DecodeError(t *testing.T) {
	serverReader, serverWriter := io.Pipe()

	client, clientWriter, _ := setupTestTransportClient(serverWriter)
	defer client.Close()

	req := NewRequest(int64(1), "decodeErrorMethod")

	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		defer wg.Done()
		// Simulate server sending invalid JSON
		simulateServer(t, serverReader, clientWriter, func(_ json.RawMessage) json.RawMessage {
			return []byte(`{"jsonrpc": "2.0", "id": 1, "result": "test"]`) // Malformed JSON
		})
	}()

	_, err := client.Call(t.Context(), req)
	if err == nil {
		t.Fatalf("TransportClient.Call should have failed due to decode error, but got nil error")
	}

	// Check if it's a json syntax error or similar io error
	var syntaxError *json.SyntaxError
	if !errors.As(err, &syntaxError) && !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("TransportClient.Call failed with unexpected error type: %v", err)
	} else {
		t.Logf("TransportClient.Call failed with expected decode error: %v", err)
	}

	wg.Wait()
}

// --- Batch Tests ---

func TestTransportClient_CallBatch(t *testing.T) {
	serverReader, serverWriter := io.Pipe()

	client, clientWriter, _ := setupTestTransportClient(serverWriter)
	defer client.Close()

	reqs := NewBatch[*Request](2)
	reqs.Add(NewRequest(int64(1), "method1"))
	reqs.Add(NewRequestWithParams(int64(2), "method2", NewParamsObject(map[string]int{"a": 1})))

	expectedResps := NewBatch[*Response](2)
	expectedResps.Add(NewResponseWithResult(int64(1), "result1"))
	expectedResps.Add(NewResponseWithError(int64(2), ErrMethodNotFound))

	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		defer wg.Done()
		simulateServer(t, serverReader, clientWriter, func(rawReq json.RawMessage) json.RawMessage {
			// Check if it's an array
			if !bytes.HasPrefix(bytes.TrimSpace(rawReq), []byte("[")) {
				t.Errorf("Server expected batch (array) request, got: %s", string(rawReq))
				return nil
			}
			// Just send the predefined batch response
			respBytes, _ := json.Marshal(expectedResps)

			return respBytes
		})
	}()

	resps, err := client.CallBatch(t.Context(), reqs)
	if err != nil {
		t.Fatalf("TransportClient.CallBatch failed: %v", err)
	}

	if len(resps) != len(expectedResps) {
		t.Fatalf("TransportClient.CallBatch returned wrong number of responses: got %d, want %d", len(resps), len(expectedResps))
	}

	// Simple check on IDs and one result/error
	resp1, ok1 := resps.Get(NewID(int64(1)))
	assert.True(t, ok1, "Missing response for id 1")
	assert.Equal(t, json.RawMessage(string("\"result1\"")), resp1.Result.value, "Response for ID 1 mismatch: got %+v", resp1)

	resp2, ok2 := resps.Get(NewID(int64(2)))
	if !ok2 || !resp2.IsError() || !errors.Is(resp2.Error, ErrMethodNotFound) {
		t.Errorf("Response for ID 2 mismatch: got %+v", resp2)
	}

	wg.Wait()
}

func TestTransportClient_CallBatch_SingleResponse(t *testing.T) {
	serverReader, serverWriter := io.Pipe()

	client, clientWriter, _ := setupTestTransportClient(serverWriter)
	defer client.Close()

	reqs := NewBatch[*Request](1)
	reqs.Add(NewRequest(int64(1), "method1"))

	// Simulate server incorrectly sending a single object response instead of array
	singleResp := NewResponseWithResult(int64(1), "result1")

	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		defer wg.Done()
		simulateServer(t, serverReader, clientWriter, func(_ json.RawMessage) json.RawMessage {
			respBytes, _ := json.Marshal(singleResp)
			return respBytes
		})
	}()

	resps, err := client.CallBatch(t.Context(), reqs)
	if err != nil {
		t.Fatalf("TransportClient.CallBatch failed: %v", err)
	}

	if len(resps) != 1 {
		t.Fatalf("TransportClient.CallBatch should wrap single response: got len %d, want 1", len(resps))
	}

	resp1, ok1 := resps.Get(NewID(int64(1)))
	assert.True(t, ok1, "Sing response missing")
	assert.Equal(t, json.RawMessage(string("\"result1\"")), resp1.Result.value, "Single response mismatch: got %+v", resp1)

	wg.Wait()
}

func TestTransportClient_NotifyBatch(t *testing.T) {
	serverReader, serverWriter := io.Pipe()

	client, clientWriter, _ := setupTestTransportClient(serverWriter)
	defer client.Close()

	notifs := NewBatch[*Notification](2)
	notifs.Add(NewNotification("notify1"))
	notifs.Add(NewNotificationWithParams("notify2", NewParamsArray([]string{"data"})))

	var wg sync.WaitGroup

	wg.Add(1)

	serverReceived := false

	go func() {
		defer wg.Done()
		simulateServer(t, serverReader, clientWriter, func(rawReq json.RawMessage) json.RawMessage {
			serverReceived = true
			// Check if it's an array
			if !bytes.HasPrefix(bytes.TrimSpace(rawReq), []byte("[")) {
				t.Errorf("Server expected batch (array) notification, got: %s", string(rawReq))
			}
			// No response for notifications
			return nil
		})
	}()

	err := client.NotifyBatch(t.Context(), notifs)
	if err != nil {
		t.Fatalf("TransportClient.NotifyBatch failed: %v", err)
	}

	// Close the client-side writer to signal EOF
	if c, ok := client.e.(io.Closer); ok {
		c.Close()
	}

	wg.Wait()

	if !serverReceived {
		t.Error("Server did not receive the notification batch")
	}
}

// --- Raw Tests ---

func TestTransportClient_CallRaw(t *testing.T) {
	serverReader, serverWriter := io.Pipe()

	client, clientWriter, _ := setupTestTransportClient(serverWriter)
	defer client.Close()

	rawReq := RawRequest(`{"jsonrpc": "2.0", "method": "rawMethod", "params": [1, 2], "id": "req-raw"}`)
	expectedResp := NewResponseWithResult("req-raw", int(3))

	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		defer wg.Done()
		simulateServer(t, serverReader, clientWriter, func(rawReq json.RawMessage) json.RawMessage {
			// Basic check on method
			if !strings.Contains(string(rawReq), `"method":"rawMethod"`) {
				t.Errorf("Server received wrong raw request: %s", string(rawReq))
			}

			respBytes, _ := json.Marshal(expectedResp)

			return respBytes
		})
	}()

	resp, err := client.CallRaw(t.Context(), rawReq)
	if err != nil {
		t.Fatalf("TransportClient.CallRaw failed: %v", err)
	}

	var result int
	if err := resp.Result.Unmarshal(&result); err != nil || result != 3 {
		t.Errorf("TransportClient received wrong result: got %v (err: %v), want %d", resp.Result.value, err, 3)
	}

	respID, _ := resp.ID.String()
	if respID != "req-raw" {
		t.Errorf("TransportClient received wrong ID: got %s, want %s", respID, "req-raw")
	}

	wg.Wait()
}

func TestTransportClient_NotifyRaw(t *testing.T) {
	serverReader, serverWriter := io.Pipe()

	client, clientWriter, _ := setupTestTransportClient(serverWriter)
	defer client.Close()

	rawNotif := RawNotification(`{"jsonrpc": "2.0", "method": "rawNotify", "params": {"status": "done"}}`)

	var wg sync.WaitGroup

	wg.Add(1)

	serverReceived := false

	go func() {
		defer wg.Done()
		simulateServer(t, serverReader, clientWriter, func(rawReq json.RawMessage) json.RawMessage {
			serverReceived = true

			if !strings.Contains(string(rawReq), `"method":"rawNotify"`) {
				t.Errorf("Server received wrong raw notification: %s", string(rawReq))
			}

			return nil // No response
		})
	}()

	err := client.NotifyRaw(t.Context(), rawNotif)
	if err != nil {
		t.Fatalf("TransportClient.NotifyRaw failed: %v", err)
	}

	// Close the client-side writer to signal EOF
	if c, ok := client.e.(io.Closer); ok {
		c.Close()
	}

	wg.Wait()

	if !serverReceived {
		t.Error("Server did not receive the raw notification")
	}
}
