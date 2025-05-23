package jsonrpc2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockWriter is a helper to simulate different writer behaviors.
type mockWriter struct {
	io.Writer
	writeErr      error
	closeFn       func() error
	closeCh       chan struct{}
	writeDeadline *time.Timer
	mu            sync.Mutex
	deadlineSet   bool
}

func (m *mockWriter) Write(p []byte) (n int, err error) {
	m.mu.Lock()
	deadline := m.deadlineSet
	writeErr := m.writeErr
	m.mu.Unlock()

	if writeErr != nil {
		return 0, writeErr
	}

	if deadline {
		<-m.writeDeadline.C
		return 0, os.ErrDeadlineExceeded
	}

	if m.closeCh != nil {
		<-m.closeCh
		return 0, io.ErrClosedPipe // Simulate writing to a closed writer
	}

	return m.Writer.Write(p)
}

func (m *mockWriter) Close() error {
	defer func() {
		if m.closeCh != nil {
			close(m.closeCh)
		}
	}()

	if m.closeFn != nil {
		return m.closeFn()
	}

	if closer, ok := m.Writer.(io.Closer); ok {
		return closer.Close()
	}

	return nil
}

func (m *mockWriter) SetWriteDeadline(t time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.writeDeadline == nil {
		m.writeDeadline = time.NewTimer(time.Until(t))
	}

	// Zero time means stop the deadline timer
	if t.IsZero() {
		m.writeDeadline.Stop()
		m.deadlineSet = true

		return nil
	}

	// Set the new deadline
	m.writeDeadline.Reset(time.Until(t))
	m.deadlineSet = true

	return nil
}

type mockWriteCloser struct {
	mock *mockWriter
}

func (mc *mockWriteCloser) Write(p []byte) (n int, err error) {
	return mc.mock.Write(p)
}

func (mc *mockWriteCloser) Close() error {
	return mc.mock.Close()
}

// Ensure mockWriter implements DeadlineWriter.
var _ DeadlineWriter = (*mockWriter)(nil)

func TestNewEncoder(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	encoder := NewEncoder(&buf)
	require.NotNil(t, encoder)

	data := map[string]string{"key": "value"}
	err := encoder.Encode(t.Context(), data)
	require.NoError(t, err)

	// Check if the output is valid JSON and contains the expected data
	var result map[string]string
	// Note: json.Encoder adds a newline
	err = json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &result)
	require.NoError(t, err)
	assert.Equal(t, "value", result["key"])
}

func TestStreamEncoder_Encode_Success(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	encoder := NewStreamEncoder(&buf)

	req := NewRequest(int64(1), "test")
	err := encoder.Encode(t.Context(), req)
	require.NoError(t, err)

	// Check output
	expectedJSON := `{"jsonrpc":"2.0","method":"test","id":1}` + "\n" // json.Encoder adds newline
	assert.JSONEq(t, strings.TrimSpace(expectedJSON), strings.TrimSpace(buf.String()))
}

func TestStreamEncoder_Encode_Multiple(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	encoder := NewStreamEncoder(&buf)

	req1 := NewRequest(int64(1), "test1")
	req2 := NewRequest(int64(2), "test2")

	err := encoder.Encode(t.Context(), req1)
	require.NoError(t, err)
	err = encoder.Encode(t.Context(), req2)
	require.NoError(t, err)

	// Check output - should be two JSON objects separated by newline
	output := buf.String()
	dec := json.NewDecoder(strings.NewReader(output))

	var res1, res2 Request
	err = dec.Decode(&res1)
	require.NoError(t, err)
	assert.True(t, req1.ID.Equal(res1.ID))
	assert.Equal(t, req1.Method, res1.Method)

	err = dec.Decode(&res2)
	require.NoError(t, err)
	assert.True(t, req2.ID.Equal(res2.ID))
	assert.Equal(t, req2.Method, res2.Method)
}

func TestStreamEncoder_SetIdleTimeout_DeadlineWriter(t *testing.T) {
	t.Parallel()
	// Use a writer that blocks indefinitely until timeout
	blockingWriter := &mockWriter{Writer: io.Discard} // io.Discard never blocks or errors
	encoder := NewStreamEncoder(blockingWriter)
	timeout := 50 * time.Millisecond
	encoder.SetIdleTimeout(timeout)

	ctx, cancel := context.WithTimeout(t.Context(), 2*timeout) // Context longer than idle timeout
	defer cancel()

	data := map[string]string{"key": "value"}
	startTime := time.Now()
	err := encoder.Encode(ctx, data)
	duration := time.Since(startTime)

	require.Error(t, err)
	// Expecting the timeout error from the mock writer or context deadline exceeded
	assert.True(t, errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded), "Expected timeout error, got: %v", err)
	assert.GreaterOrEqual(t, duration, timeout, "Encode should have taken at least the idle timeout duration")
	assert.Less(t, duration, 2*timeout, "Encode should not have waited for the full context timeout")
}

func TestStreamEncoder_SetIdleTimeout_Closer(t *testing.T) {
	t.Parallel()

	closed := false
	closeFn := func() error {
		closed = true
		return nil
	}
	// Use a writer that blocks, but implement Close
	// Use io.Discard as the base writer, mockWriter handles blocking/closing
	blockingWriter := &mockWriteCloser{mock: &mockWriter{Writer: io.Discard, closeFn: closeFn, closeCh: make(chan struct{})}}
	encoder := NewStreamEncoder(blockingWriter)
	timeout := 50 * time.Millisecond
	encoder.SetIdleTimeout(timeout)

	ctx, cancel := context.WithTimeout(t.Context(), 2*timeout)
	defer cancel()

	data := map[string]string{"key": "value"}
	startTime := time.Now()
	err := encoder.Encode(ctx, data)
	duration := time.Since(startTime)

	require.Error(t, err)
	// When using Close(), the Encode often returns io.ErrClosedPipe or similar after close.
	// The primary check is that Close was called and it happened around the timeout.
	assert.True(t, closed, "Writer Close() should have been called")
	assert.GreaterOrEqual(t, duration, timeout, "Encode should have taken at least the idle timeout duration")
	assert.Less(t, duration, 2*timeout, "Encode should not have waited for the full context timeout")
	assert.ErrorIs(t, err, context.DeadlineExceeded) // Joined error should include context deadline
}

func TestStreamEncoder_SetIdleTimeout_NoSupport(t *testing.T) {
	t.Parallel()
	// Use a simple writer with no Close or SetWriteDeadline
	var buf bytes.Buffer // bytes.Buffer doesn't support deadlines or closing in the required way
	encoder := NewStreamEncoder(&buf)
	timeout := 50 * time.Millisecond
	encoder.SetIdleTimeout(timeout) // Should have no effect on Encode timing itself

	ctx, cancel := context.WithTimeout(t.Context(), 2*timeout)
	defer cancel()

	data := map[string]string{"key": "value"}
	encodeErrChan := make(chan error, 1)
	startTime := time.Now()

	go func() {
		encodeErrChan <- encoder.Encode(ctx, data)
	}()

	select {
	case err := <-encodeErrChan:
		duration := time.Since(startTime)
		// Encode should complete quickly as bytes.Buffer doesn't block
		require.NoError(t, err)
		assert.Less(t, duration, timeout, "Encode should complete faster than the idle timeout")
		// Verify data was written
		assert.Contains(t, buf.String(), `"key":"value"`)
	case <-ctx.Done():
		assert.Fail(t, "Context timed out, Encode should have completed quickly")
	case <-time.After(timeout + 10*time.Millisecond):
		// If we wait slightly longer than the idle timeout and Encode hasn't returned,
		// it implies the timeout mechanism isn't working (as expected for this writer type).
		// However, for bytes.Buffer, Encode should have returned almost instantly.
		// This case primarily tests that the timeout logic doesn't *incorrectly* interfere
		// when no timeout mechanism is available.
		assert.Fail(t, "Encode took longer than expected, timeout logic might be interfering incorrectly")
	}
}

func TestStreamEncoder_Encode_ContextCancellation(t *testing.T) {
	t.Parallel()
	// Use a writer that blocks indefinitely via SetWriteDeadline
	blockingWriter := &mockWriter{Writer: io.Discard}
	encoder := NewStreamEncoder(blockingWriter)
	// Set an initial long deadline to make it block
	require.NoError(t, blockingWriter.SetWriteDeadline(time.Now().Add(1*time.Hour)))

	ctx, cancel := context.WithCancel(t.Context())

	data := map[string]string{"key": "value"}
	errChan := make(chan error, 1)

	go func() {
		errChan <- encoder.Encode(ctx, data)
	}()

	// Wait a moment to ensure Encode has started and potentially blocked
	time.Sleep(20 * time.Millisecond)

	// Cancel the context
	cancel()

	// Wait for Encode to return
	select {
	case err := <-errChan:
		require.Error(t, err)
		// Depending on timing, it could be Canceled or DeadlineExceeded
		assert.True(t, errors.Is(err, context.Canceled) || errors.Is(err, os.ErrDeadlineExceeded), "Expected Canceled or DeadlineExceeded, got: %v", err)
	case <-time.After(1 * time.Second):
		assert.Fail(t, "Encode did not return after context cancellation")
	}
}

func TestStreamEncoder_Close(t *testing.T) {
	t.Parallel()

	// Test with a closer
	closed := false
	closeFn := func() error {
		closed = true
		return nil
	}
	writerWithClose := &mockWriter{Writer: io.Discard, closeFn: closeFn}
	encoderWithClose := NewStreamEncoder(writerWithClose)
	err := encoderWithClose.Close()
	require.NoError(t, err)
	assert.True(t, closed, "Close should have been called on the underlying writer")

	// Test with a non-closer
	writerWithoutClose := &bytes.Buffer{} // bytes.Buffer is not an io.Closer
	encoderWithoutClose := NewStreamEncoder(writerWithoutClose)
	err = encoderWithoutClose.Close()
	require.NoError(t, err, "Close should return nil for non-closer writers")
}

func TestStreamEncoder_Encode_Error(t *testing.T) {
	t.Parallel()

	// Test underlying writer error
	writeErr := errors.New("write failed")
	errorWriter := &mockWriter{Writer: io.Discard, writeErr: writeErr}
	encoder := NewStreamEncoder(errorWriter)
	data := map[string]string{"key": "value"}
	err := encoder.Encode(t.Context(), data)
	require.Error(t, err)
	assert.ErrorIs(t, err, writeErr)

	// Test JSON encoding error (e.g., encoding a channel)
	var buf bytes.Buffer
	encoder = NewStreamEncoder(&buf)
	chanData := make(chan int)
	err = encoder.Encode(t.Context(), chanData)
	require.Error(t, err)

	var jsonErr *json.UnsupportedTypeError

	assert.True(t, errors.As(err, &jsonErr), "Expected a json.UnsupportedTypeError")
}
