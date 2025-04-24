package jsonrpc2

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Ensure mockPacketConn implements net.PacketConn.
var _ net.PacketConn = (*mockPacketConn)(nil)

func TestNewPacketEncoder(t *testing.T) {
	t.Parallel()

	conn := newMockPacketConn()
	defer conn.Close()

	encoder := NewPacketEncoder(conn)
	require.NotNil(t, encoder)

	// Encode some data
	data := map[string]string{"key": "value"}
	err := encoder.EncodeTo(t.Context(), data, conn.remoteAddr)
	require.NoError(t, err)

	// Verify data written
	writtenData, err := conn.ReceiveData(100 * time.Millisecond)
	require.NoError(t, err, "Did not receive data on write channel")

	var result map[string]string
	err = json.Unmarshal(writtenData, &result)
	require.NoError(t, err, "Failed to unmarshal written data")
	assert.Equal(t, "value", result["key"])
}

func TestPacketConnEncoder_EncodeTo_Success(t *testing.T) {
	t.Parallel()

	conn := newMockPacketConn()
	defer conn.Close()
	encoder := NewPacketConnEncoder(conn)

	req := NewRequest(int64(1), "test")
	err := encoder.EncodeTo(t.Context(), req, conn.remoteAddr)
	require.NoError(t, err)

	// Verify data written
	writtenData, err := conn.ReceiveData(100 * time.Millisecond)
	require.NoError(t, err)

	var writtenReq Request
	err = json.Unmarshal(writtenData, &writtenReq)
	require.NoError(t, err)
	assert.True(t, req.ID.Equal(writtenReq.ID))
	assert.Equal(t, req.Method, writtenReq.Method)
}

func TestPacketConnEncoder_SetIdleTimeout(t *testing.T) {
	t.Parallel()

	conn := newMockPacketConn()
	// Do not close conn immediately
	encoder := NewPacketConnEncoder(conn)
	timeout := 50 * time.Millisecond
	encoder.SetIdleTimeout(timeout)

	ctx, cancel := context.WithTimeout(t.Context(), 2*timeout) // Context longer than idle timeout
	defer cancel()

	data := map[string]string{"key": "value"}

	// Make WriteTo block until deadline
	conn.mu.Lock()
	conn.writeErr = errors.New("force block") // Use a specific error to force blocking path in mock
	conn.mu.Unlock()

	startTime := time.Now()
	err := encoder.EncodeTo(ctx, data, conn.remoteAddr)
	duration := time.Since(startTime)

	// Close connection after test finishes or times out
	defer conn.Close()

	require.Error(t, err)
	// Expecting the timeout error from the mock connection or context deadline exceeded
	assert.True(t, errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded), "Expected timeout error, got: %v", err)
	assert.GreaterOrEqual(t, duration, timeout, "EncodeTo should have taken at least the idle timeout duration")
	assert.Less(t, duration, 2*timeout, "EncodeTo should not have waited for the full context timeout")

	// Verify SetWriteDeadline was called correctly
	conn.mu.Lock()
	deadlineWasSet := conn.writeDeadlineSet
	conn.mu.Unlock()
	assert.True(t, deadlineWasSet, "SetWriteDeadline should have been called with a non-zero time")
}

func TestPacketConnEncoder_EncodeTo_ContextCancellation(t *testing.T) {
	t.Parallel()

	conn := newMockPacketConn()
	defer conn.Close() // Ensure closed eventually
	encoder := NewPacketConnEncoder(conn)

	ctx, cancel := context.WithCancel(t.Context())

	data := map[string]string{"key": "value"}
	errChan := make(chan error, 1)

	// Make WriteTo block indefinitely to test cancellation
	conn.mu.Lock()
	conn.writeErr = errors.New("force block") // Use a specific error to force blocking path in mock
	conn.mu.Unlock()
	// Set a long write deadline in the mock directly to ensure it blocks
	_ = conn.SetWriteDeadline(time.Now().Add(1 * time.Hour))

	go func() {
		errChan <- encoder.EncodeTo(ctx, data, conn.remoteAddr)
	}()

	// Wait a moment to ensure EncodeTo has started and might be blocking
	time.Sleep(20 * time.Millisecond)

	// Cancel the context
	cancel()

	// Wait for EncodeTo to return
	select {
	case err := <-errChan:
		require.Error(t, err)
		// The error should include context.Canceled because the AfterFunc triggers SetWriteDeadline(now)
		// which causes WriteTo to return immediately (likely with DeadlineExceeded),
		// and the outer function joins the context error.
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(1 * time.Second):
		assert.Fail(t, "EncodeTo did not return after context cancellation")
	}
}

func TestPacketConnEncoder_Close(t *testing.T) {
	t.Parallel()

	conn := newMockPacketConn()
	encoder := NewPacketConnEncoder(conn)

	// Check initial state
	conn.mu.Lock()
	closed := conn.closed
	conn.mu.Unlock()
	assert.False(t, closed, "Connection should not be closed initially")

	err := encoder.Close()
	require.NoError(t, err)

	// Check state after close
	conn.mu.Lock()
	closed = conn.closed
	conn.mu.Unlock()
	assert.True(t, closed, "Close should have been called on the underlying connection")

	// Test double close
	err = encoder.Close()
	require.NoError(t, err, "Double close should be allowed and return nil or net.ErrClosed") // Allow nil or ErrClosed
}

func TestPacketConnEncoder_EncodeTo_MarshalError(t *testing.T) {
	t.Parallel()

	conn := newMockPacketConn()
	defer conn.Close()
	encoder := NewPacketConnEncoder(conn)

	// Data that cannot be marshaled (e.g., a channel)
	invalidData := make(chan int)

	err := encoder.EncodeTo(t.Context(), invalidData, conn.remoteAddr)
	require.Error(t, err)

	var jsonErr *json.UnsupportedTypeError
	ok := errors.As(err, &jsonErr)
	assert.True(t, ok, "Expected a json.UnsupportedTypeError")

	// Ensure nothing was written
	_, err = conn.ReceiveData(10 * time.Millisecond) // Short timeout
	assert.Error(t, err, "No data should have been written on marshal error")
}

func TestPacketConnEncoder_EncodeTo_WriteError(t *testing.T) {
	t.Parallel()

	conn := newMockPacketConn()
	defer conn.Close()
	encoder := NewPacketConnEncoder(conn)

	expectedErr := errors.New("simulated write error")

	conn.mu.Lock()
	conn.writeErr = expectedErr
	conn.mu.Unlock()

	data := map[string]string{"key": "value"}
	err := encoder.EncodeTo(t.Context(), data, conn.remoteAddr)

	require.Error(t, err)
	assert.ErrorIs(t, err, expectedErr)
}
