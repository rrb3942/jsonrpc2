package jsonrpc2

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockPacketConn simulates a net.PacketConn for testing.
type mockPacketConn struct {
	readChan         chan []byte
	writeChan        chan []byte
	closeChan        chan struct{}
	readDeadline     *time.Timer
	writeDeadline    *time.Timer
	localAddr        net.Addr
	remoteAddr       net.Addr
	readErr          error // Error to return on ReadFrom
	writeErr         error // Error to return on WriteTo
	closeErr         error // Error to return on Close
	mu               sync.Mutex
	closed           bool
	readDeadlineSet  bool // Tracks if SetReadDeadline was called with non-zero time
	writeDeadlineSet bool // Tracks if SetReadDeadline was called with non-zero time
}

func newMockPacketConn() *mockPacketConn {
	return &mockPacketConn{
		readChan:   make(chan []byte, 1), // Buffered to allow sending before read
		writeChan:  make(chan []byte, 1),
		closeChan:  make(chan struct{}),
		localAddr:  &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234},
		remoteAddr: &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5678},
	}
}

func (m *mockPacketConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	m.mu.Lock()
	deadline := m.readDeadline
	readErr := m.readErr
	closed := m.closed
	m.mu.Unlock()

	if closed {
		return 0, nil, net.ErrClosed
	}

	if readErr != nil {
		return 0, m.remoteAddr, readErr
	}

	select {
	case <-m.closeChan:
		return 0, nil, net.ErrClosed
	case data := <-m.readChan:
		n = copy(p, data)
		return n, m.remoteAddr, nil
	case <-deadline.C:
		return 0, nil, os.ErrDeadlineExceeded
	}
}

func (m *mockPacketConn) WriteTo(p []byte, _ net.Addr) (n int, err error) {
	m.mu.Lock()
	deadline := m.writeDeadline
	writeErr := m.writeErr
	closed := m.closed
	m.mu.Unlock()

	if closed {
		return 0, net.ErrClosed
	}

	if writeErr != nil {
		if writeErr.Error() == "force block" {
			<-deadline.C
			return 0, os.ErrDeadlineExceeded
		}

		return 0, writeErr
	}

	select {
	case <-m.closeChan:
		return 0, net.ErrClosed
	case m.writeChan <- p:
		return len(p), nil
	case <-deadline.C:
		return 0, os.ErrDeadlineExceeded
	}
}

func (m *mockPacketConn) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return m.closeErr // Typically net.ErrClosed if already closed
	}

	m.closed = true
	close(m.closeChan)

	if m.readDeadline != nil {
		m.readDeadline.Stop()
	}

	if m.writeDeadline != nil {
		m.writeDeadline.Stop()
	}

	return m.closeErr
}

func (m *mockPacketConn) LocalAddr() net.Addr {
	return m.localAddr
}

func (m *mockPacketConn) SetDeadline(t time.Time) error {
	if err := m.SetReadDeadline(t); err != nil {
		return err
	}

	return m.SetWriteDeadline(t)
}

func (m *mockPacketConn) SetReadDeadline(t time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.readDeadline == nil {
		// Initialize timer in a stopped state
		m.readDeadline = time.NewTimer(time.Until(t)) // Long duration, will be reset
	}

	// Zero time means stop
	if t.IsZero() {
		m.readDeadline.Stop()
		m.readDeadlineSet = false // Mark deadline as cleared
	} else {
		m.readDeadline.Reset(time.Until(t))
		m.readDeadlineSet = true // Mark deadline as active
	}

	return nil
}

func (m *mockPacketConn) SetWriteDeadline(t time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.writeDeadline == nil {
		// Initialize timer in a stopped state
		m.writeDeadline = time.NewTimer(time.Until(t)) // Long duration, will be reset
	}

	// Zero time means stop
	if t.IsZero() {
		m.writeDeadline.Stop()
		m.writeDeadlineSet = false
	} else {
		m.writeDeadline.Reset(time.Until(t))
		m.writeDeadlineSet = true
	}

	return nil
}

// Helper to send data to the mock connection.
func (m *mockPacketConn) SendData(data []byte) {
	m.readChan <- data
}

// Helper to receive data from the mock connection's write channel.
func (m *mockPacketConn) ReceiveData(timeout time.Duration) ([]byte, error) {
	select {
	case data := <-m.writeChan:
		return data, nil
	case <-time.After(timeout):
		return nil, errors.New("timeout waiting for data on write channel")
	}
}

// Ensure mockPacketConn implements net.PacketConn.
var _ net.PacketConn = (*mockPacketConn)(nil)

func TestNewPacketDecoder(t *testing.T) {
	t.Parallel()

	conn := newMockPacketConn()
	defer conn.Close()

	decoder := NewPacketDecoder(conn)
	require.NotNil(t, decoder)

	// Send some data
	jsonData := `{"key": "value"}`
	conn.SendData([]byte(jsonData))

	var result map[string]string
	addr, err := decoder.DecodeFrom(t.Context(), &result)
	require.NoError(t, err)
	assert.Equal(t, conn.remoteAddr, addr)
	assert.Equal(t, "value", result["key"])
}

func TestPacketConnDecoder_DecodeFrom_Success(t *testing.T) {
	t.Parallel()

	conn := newMockPacketConn()
	defer conn.Close()
	decoder := NewPacketConnDecoder(conn)

	jsonData := `{"jsonrpc": "2.0", "id": 1, "method": "test"}`
	conn.SendData([]byte(jsonData))

	var req Request
	addr, err := decoder.DecodeFrom(t.Context(), &req)
	require.NoError(t, err)
	assert.Equal(t, conn.remoteAddr, addr)
	assert.Equal(t, json.Number("1"), req.ID.value)
	assert.Equal(t, "test", req.Method)
}

func TestPacketConnDecoder_SetIdleTimeout(t *testing.T) {
	t.Parallel()

	conn := newMockPacketConn()
	// Do not close conn immediately, let the timeout handle it or the test end
	decoder := NewPacketConnDecoder(conn)
	timeout := 50 * time.Millisecond
	decoder.SetIdleTimeout(timeout)

	ctx, cancel := context.WithTimeout(t.Context(), 2*timeout) // Context longer than idle timeout
	defer cancel()

	var result map[string]string

	startTime := time.Now()
	addr, err := decoder.DecodeFrom(ctx, &result)
	duration := time.Since(startTime)

	// Close connection after test finishes or times out
	defer conn.Close()

	require.Error(t, err)
	assert.Nil(t, addr) // No address received on timeout
	// Expecting the timeout error from the mock connection or context deadline exceeded
	assert.True(t, errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded), "Expected timeout error, got: %v", err)
	assert.GreaterOrEqual(t, duration, timeout, "DecodeFrom should have taken at least the idle timeout duration")
	assert.Less(t, duration, 2*timeout, "DecodeFrom should not have waited for the full context timeout")

	// Verify SetReadDeadline was called correctly
	conn.mu.Lock()
	deadlineWasSet := conn.readDeadlineSet
	conn.mu.Unlock()
	assert.True(t, deadlineWasSet, "SetReadDeadline should have been called with a non-zero time")
}

func TestPacketConnDecoder_DecodeFrom_ContextCancellation(t *testing.T) {
	t.Parallel()

	conn := newMockPacketConn()
	defer conn.Close() // Ensure closed eventually
	decoder := NewPacketConnDecoder(conn)

	ctx, cancel := context.WithCancel(t.Context())

	var result map[string]string

	errChan := make(chan error, 1)
	addrChan := make(chan net.Addr, 1)

	go func() {
		addr, err := decoder.DecodeFrom(ctx, &result)
		addrChan <- addr
		errChan <- err
	}()

	// Wait a moment to ensure DecodeFrom has started and might be blocking
	time.Sleep(20 * time.Millisecond)

	// Cancel the context
	cancel()

	// Wait for DecodeFrom to return
	select {
	case err := <-errChan:
		addr := <-addrChan

		require.Error(t, err)
		assert.Nil(t, addr)
		// The error should include context.Canceled because the AfterFunc triggers SetReadDeadline(now)
		// which causes ReadFrom to return immediately (likely with 0 bytes or an error),
		// and the outer function joins the context error.
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(1 * time.Second):
		assert.Fail(t, "DecodeFrom did not return after context cancellation")
	}
}

func TestPacketConnDecoder_Unmarshal(t *testing.T) {
	conn := newMockPacketConn() // Connection not strictly needed but follows pattern
	defer conn.Close()
	decoder := NewPacketConnDecoder(conn)

	jsonData := []byte(`{"field": "value"}`)

	type testStruct struct {
		Field string `json:"field"`
	}

	var result testStruct

	// Use a custom unmarshaler to verify it's called
	originalUnmarshal := Unmarshal
	unmarshalCalled := false
	Unmarshal = func(data []byte, v any) error {
		unmarshalCalled = true
		// Use standard unmarshal for the actual test
		// Need to copy data as the original buffer might be reused
		bufCopy := make([]byte, len(data))
		copy(bufCopy, data)

		return json.Unmarshal(bufCopy, v)
	}

	defer func() { Unmarshal = originalUnmarshal }() // Restore original

	err := decoder.Unmarshal(jsonData, &result)
	require.NoError(t, err)
	assert.True(t, unmarshalCalled, "Custom Unmarshal should have been called")
	assert.Equal(t, "value", result.Field)
}

func TestPacketConnDecoder_Close(t *testing.T) {
	t.Parallel()

	conn := newMockPacketConn()
	decoder := NewPacketConnDecoder(conn)

	// Check initial state
	conn.mu.Lock()
	closed := conn.closed
	conn.mu.Unlock()
	assert.False(t, closed, "Connection should not be closed initially")

	err := decoder.Close()
	require.NoError(t, err)

	// Check state after close
	conn.mu.Lock()
	closed = conn.closed
	conn.mu.Unlock()
	assert.True(t, closed, "Close should have been called on the underlying connection")

	// Test double close
	err = decoder.Close()
	require.NoError(t, err, "Double close should be allowed and return nil or net.ErrClosed") // Allow nil or ErrClosed
}

func TestPacketConnDecoder_DecodeFrom_InvalidJSON(t *testing.T) {
	t.Parallel()

	conn := newMockPacketConn()
	defer conn.Close()
	decoder := NewPacketConnDecoder(conn)

	invalidJSON := `{"key": "value"]` // Unbalanced { ]
	conn.SendData([]byte(invalidJSON))

	var result map[string]string
	addr, err := decoder.DecodeFrom(t.Context(), &result)
	require.Error(t, err)
	assert.Equal(t, conn.remoteAddr, addr) // Address should still be returned

	var syntaxError *json.SyntaxError
	ok := errors.As(err, &syntaxError)
	assert.True(t, ok, "Expected a json.SyntaxError")
}

func TestPacketConnDecoder_DecodeFrom_EmptyPacket(t *testing.T) {
	t.Parallel()

	conn := newMockPacketConn()
	defer conn.Close()
	decoder := NewPacketConnDecoder(conn)

	conn.SendData([]byte{}) // Send empty packet

	var result map[string]string
	addr, err := decoder.DecodeFrom(t.Context(), &result)
	require.Error(t, err) // Expect error because empty data is not valid JSON
	assert.Equal(t, conn.remoteAddr, addr)
	// The specific error might be io.EOF from json decoder or a syntax error
	var syntaxError *json.SyntaxError

	assert.True(t, errors.Is(err, io.EOF) || errors.As(err, &syntaxError), "Expected EOF or json.SyntaxError for empty packet, got: %v", err)
}

func TestPacketConnDecoder_DecodeFrom_ReadError(t *testing.T) {
	t.Parallel()

	conn := newMockPacketConn()
	defer conn.Close()
	decoder := NewPacketConnDecoder(conn)

	expectedErr := errors.New("simulated read error")

	conn.mu.Lock()
	conn.readErr = expectedErr
	conn.mu.Unlock()

	var result map[string]string
	addr, err := decoder.DecodeFrom(t.Context(), &result)

	require.Error(t, err)
	assert.Equal(t, conn.remoteAddr, addr) // Address might still be returned with error
	assert.ErrorIs(t, err, expectedErr)
}

func TestPacketConnDecoder_DecodeFrom_BufferLimit(t *testing.T) {
	t.Parallel()

	conn := newMockPacketConn()
	defer conn.Close()
	decoder := NewPacketConnDecoder(conn)

	// Default buffer size is DefaultPacketBufferSize (65507)
	// Let's send data smaller than that
	jsonData := `{"data": "fits within default buffer"}`
	conn.SendData([]byte(jsonData))

	var result map[string]string
	addr, err := decoder.DecodeFrom(t.Context(), &result)
	require.NoError(t, err)
	assert.Equal(t, conn.remoteAddr, addr)
	assert.Equal(t, "fits within default buffer", result["data"])

	// Test with a custom (smaller) buffer size via SetLimit (although PacketConnDecoder doesn't use it directly for buffer allocation size)
	// Instead, let's test the readSize logic inside DecodeFrom
	customLimit := int64(10)
	decoder.SetLimit(customLimit) // This sets i.n

	smallJSONData := `{"a":1}` // Fits in 10 bytes
	conn.SendData([]byte(smallJSONData))

	var smallResult map[string]int
	addr, err = decoder.DecodeFrom(t.Context(), &smallResult)
	require.NoError(t, err, "Should decode successfully as read buffer uses the limit")
	assert.Equal(t, conn.remoteAddr, addr)
	assert.Equal(t, 1, smallResult["a"])

	// Now send data larger than the custom limit
	largeJSONData := `{"key": "this is definitely larger than 10 bytes"}`
	conn.SendData([]byte(largeJSONData))

	var largeResult map[string]string
	addr, err = decoder.DecodeFrom(t.Context(), &largeResult)
	// ReadFrom reads into a buffer of size `readSize` (which is `i.n` if set > 0).
	// If the actual packet is larger, ReadFrom might truncate or error depending on the OS/impl.
	// Our mock simply copies up to the buffer size. The json.Unmarshal will likely fail.
	require.Error(t, err, "Expected error decoding truncated JSON")
	assert.Equal(t, conn.remoteAddr, addr) // Address is received before unmarshal error

	var syntaxError *json.SyntaxError
	ok := errors.As(err, &syntaxError)
	assert.True(t, ok, "Expected a json.SyntaxError due to truncation")
}
