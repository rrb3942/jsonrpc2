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

// mockReader is a helper to simulate different reader behaviors.
type mockReader struct {
	io.Reader
	closeFn      func() error
	closeCh      chan struct{}
	readDeadline *time.Timer
	mu           sync.Mutex
	deadlineSet  bool
}

func (m *mockReader) Read(p []byte) (n int, err error) {
	m.mu.Lock()
	deadline := m.deadlineSet
	m.mu.Unlock()

	if deadline {
		<-m.readDeadline.C
		return 0, os.ErrDeadlineExceeded
	}

	if m.closeCh != nil {
		<-m.closeCh
		return 0, io.EOF
	}

	return m.Reader.Read(p)
}

func (m *mockReader) Close() error {
	defer func() {
		if m.closeCh != nil {
			close(m.closeCh)
		}
	}()

	if m.closeFn != nil {
		return m.closeFn()
	}

	if closer, ok := m.Reader.(io.Closer); ok {
		return closer.Close()
	}

	return nil
}

func (m *mockReader) SetReadDeadline(t time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.readDeadline == nil {
		m.readDeadline = time.NewTimer(time.Until(t))
	}

	// Zero time means stop
	if t.Equal(time.Time{}) {
		m.readDeadline.Stop()
		m.deadlineSet = true

		return nil
	}

	m.readDeadline.Reset(time.Until(t))
	m.deadlineSet = true

	return nil
}

type mockReadCloser struct {
	mock *mockReader
}

func (mc *mockReadCloser) Read(p []byte) (n int, err error) {
	return mc.mock.Read(p)
}

func (mc *mockReadCloser) Close() error {
	return mc.mock.Close()
}

// Ensure mockReader implements DeadlineReader.
var _ DeadlineReader = (*mockReader)(nil)

func TestNewDecoder(t *testing.T) {
	t.Parallel()

	r := strings.NewReader(`{"key": "value"}`)
	decoder := NewDecoder(r)
	require.NotNil(t, decoder)

	var result map[string]string
	err := decoder.Decode(t.Context(), &result)
	require.NoError(t, err)
	assert.Equal(t, "value", result["key"])
}

func TestStreamDecoder_Decode_Success(t *testing.T) {
	t.Parallel()

	r := strings.NewReader(`{"jsonrpc": "2.0", "id": 1, "method": "test"}`)
	decoder := NewStreamDecoder(r)

	var req Request
	err := decoder.Decode(t.Context(), &req)
	require.NoError(t, err)
	assert.Equal(t, json.Number("1"), req.ID.value)
	assert.Equal(t, "test", req.Method)
}

func TestStreamDecoder_Decode_Multiple(t *testing.T) {
	t.Parallel()

	r := strings.NewReader(`{"id": 1}{"id": 2}`)
	decoder := NewStreamDecoder(r)

	var req1, req2 Request

	err := decoder.Decode(t.Context(), &req1)
	require.NoError(t, err)
	assert.Equal(t, json.Number("1"), req1.ID.value)

	err = decoder.Decode(t.Context(), &req2)
	require.NoError(t, err)
	assert.Equal(t, json.Number("2"), req2.ID.value)
}

func TestStreamDecoder_SetLimit(t *testing.T) {
	t.Parallel()

	jsonData := `{"key": "this is a long value"}`
	limit := int64(10)
	r := strings.NewReader(jsonData)
	decoder := NewStreamDecoder(r)
	decoder.SetLimit(limit)

	var result map[string]string
	err := decoder.Decode(t.Context(), &result)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrJSONTooLarge)

	// Test with limit large enough
	r = strings.NewReader(jsonData)
	decoder = NewStreamDecoder(r)
	decoder.SetLimit(int64(len(jsonData))) // Exact limit
	err = decoder.Decode(t.Context(), &result)
	require.NoError(t, err)
	assert.Equal(t, "this is a long value", result["key"])
}

func TestStreamDecoder_SetIdleTimeout_DeadlineReader(t *testing.T) {
	t.Parallel()
	// Use a reader that blocks indefinitely until timeout
	blockingReader := &mockReader{Reader: bytes.NewReader(nil)} // Empty reader, Read will block if called
	decoder := NewStreamDecoder(blockingReader)
	timeout := 50 * time.Millisecond
	decoder.SetIdleTimeout(timeout)

	ctx, cancel := context.WithTimeout(t.Context(), 2*timeout) // Context longer than idle timeout
	defer cancel()

	var result map[string]string

	startTime := time.Now()
	err := decoder.Decode(ctx, &result)
	duration := time.Since(startTime)

	require.Error(t, err)
	// Expecting the timeout error from the mock reader or context deadline exceeded
	assert.True(t, errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded), "Expected timeout error, got: %v", err)
	assert.GreaterOrEqual(t, duration, timeout, "Decode should have taken at least the idle timeout duration")
	assert.Less(t, duration, 2*timeout, "Decode should not have waited for the full context timeout")
}

func TestStreamDecoder_SetIdleTimeout_Closer(t *testing.T) {
	t.Parallel()

	closed := false
	closeFn := func() error {
		closed = true
		return nil
	}
	// Use a reader that blocks, but implement Close
	blockingReader := &mockReadCloser{mock: &mockReader{Reader: &bytes.Buffer{}, closeFn: closeFn, closeCh: make(chan struct{})}} // Will block on Read
	decoder := NewStreamDecoder(blockingReader)
	timeout := 50 * time.Millisecond
	decoder.SetIdleTimeout(timeout)

	ctx, cancel := context.WithTimeout(t.Context(), 2*timeout)
	defer cancel()

	var result map[string]string

	startTime := time.Now()
	err := decoder.Decode(ctx, &result)
	duration := time.Since(startTime)

	require.Error(t, err)
	// When using Close(), the Decode often returns io.EOF or similar after close.
	// The primary check is that Close was called and it happened around the timeout.
	assert.True(t, closed, "Reader Close() should have been called")
	assert.GreaterOrEqual(t, duration, timeout, "Decode should have taken at least the idle timeout duration")
	assert.Less(t, duration, 2*timeout, "Decode should not have waited for the full context timeout")
	assert.ErrorIs(t, err, context.DeadlineExceeded) // Joined error should include context deadline
}

func TestStreamDecoder_SetIdleTimeout_NoSupport(t *testing.T) {
	t.Parallel()
	// Use a simple reader with no Close or SetReadDeadline
	blockingReader := &bytes.Buffer{} // Will block on Read
	decoder := NewStreamDecoder(blockingReader)
	timeout := 50 * time.Millisecond
	decoder.SetIdleTimeout(timeout) // Should have no effect

	ctx, cancel := context.WithTimeout(t.Context(), 2*timeout)
	defer cancel()

	var result map[string]string

	decodeErrChan := make(chan error, 1)

	go func() {
		decodeErrChan <- decoder.Decode(ctx, &result)
	}()

	select {
	case err := <-decodeErrChan:
		// If Decode returns early (e.g., EOF on empty buffer), that's okay, but it shouldn't be a timeout error.
		require.Error(t, err) // Expecting EOF or similar, not timeout
		assert.NotContains(t, err.Error(), "i/o timeout")
		assert.False(t, errors.Is(err, context.DeadlineExceeded))
	case <-ctx.Done():
		// If the context times out first, it means the idle timeout didn't trigger early.
		assert.Fail(t, "Context timed out, indicating idle timeout did not trigger early as expected")
	case <-time.After(timeout + 10*time.Millisecond):
		// If we wait slightly longer than the idle timeout and Decode hasn't returned,
		// it implies the timeout mechanism isn't working (as expected for this reader type).
		// Cancel the context to unblock Decode.
		cancel()

		err := <-decodeErrChan // Wait for Decode to return after cancellation
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled) // Expect context canceled error now
	}
}

func TestStreamDecoder_Decode_ContextCancellation(t *testing.T) {
	t.Parallel()
	// Use a reader that blocks indefinitely
	blockingReader := &mockReader{Reader: &bytes.Buffer{}}
	decoder := NewStreamDecoder(blockingReader)

	ctx, cancel := context.WithCancel(t.Context())

	var result map[string]string

	errChan := make(chan error, 1)

	go func() {
		errChan <- decoder.Decode(ctx, &result)
	}()

	// Wait a moment to ensure Decode has started
	time.Sleep(20 * time.Millisecond)

	// Cancel the context
	cancel()

	// Wait for Decode to return
	select {
	case err := <-errChan:
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(1 * time.Second):
		assert.Fail(t, "Decode did not return after context cancellation")
	}
}

func TestStreamDecoder_Unmarshal(t *testing.T) {
	t.Parallel()

	decoder := NewStreamDecoder(nil) // Reader doesn't matter for Unmarshal

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
		return json.Unmarshal(data, v) // Use standard unmarshal
	}

	defer func() { Unmarshal = originalUnmarshal }() // Restore original

	err := decoder.Unmarshal(jsonData, &result)
	require.NoError(t, err)
	assert.True(t, unmarshalCalled, "Custom Unmarshal should have been called")
	assert.Equal(t, "value", result.Field)
}

func TestStreamDecoder_Close(t *testing.T) {
	t.Parallel()

	// Test with a closer
	closed := false
	closeFn := func() error {
		closed = true
		return nil
	}
	readerWithClose := &mockReader{Reader: strings.NewReader(""), closeFn: closeFn}
	decoderWithClose := NewStreamDecoder(readerWithClose)
	err := decoderWithClose.Close()
	require.NoError(t, err)
	assert.True(t, closed, "Close should have been called on the underlying reader")

	// Test with a non-closer
	readerWithoutClose := strings.NewReader("")
	decoderWithoutClose := NewStreamDecoder(readerWithoutClose)
	err = decoderWithoutClose.Close()
	require.NoError(t, err, "Close should return nil for non-closer readers")
}

func TestStreamDecoder_Decode_InvalidJSON(t *testing.T) {
	t.Parallel()

	r := strings.NewReader(`{"key": "value"]`) // Unbalanced { ]
	decoder := NewStreamDecoder(r)

	var result map[string]string
	err := decoder.Decode(t.Context(), &result)
	require.Error(t, err)

	var syntaxError *json.SyntaxError
	ok := errors.As(err, &syntaxError)
	assert.True(t, ok, "Expected a json.SyntaxError")
}

func TestStreamDecoder_Decode_EOF(t *testing.T) {
	t.Parallel()

	r := strings.NewReader("") // Empty reader
	decoder := NewStreamDecoder(r)

	var result map[string]string
	err := decoder.Decode(t.Context(), &result)
	require.Error(t, err)
	assert.Equal(t, io.EOF, err)

	// Test EOF after one successful decode
	r = strings.NewReader(`{"id": 1}`)
	decoder = NewStreamDecoder(r)

	var req Request
	err = decoder.Decode(t.Context(), &req)
	require.NoError(t, err)
	assert.Equal(t, json.Number("1"), req.ID.value)

	err = decoder.Decode(t.Context(), &result) // Try decoding again
	require.Error(t, err)
	assert.Equal(t, io.EOF, err)
}

func TestStreamDecoder_Decode_LimitEOF(t *testing.T) {
	t.Parallel()
	// Test case where EOF happens exactly at the limit boundary
	jsonData := `{"key":"val"}`
	limit := int64(len(jsonData))
	r := strings.NewReader(jsonData)
	decoder := NewStreamDecoder(r)
	decoder.SetLimit(limit)

	var result map[string]string
	err := decoder.Decode(t.Context(), &result)
	require.NoError(t, err) // Should successfully decode the object
	assert.Equal(t, "val", result["key"])

	// Next decode should be EOF, not ErrJSONTooLarge
	err = decoder.Decode(t.Context(), &result)
	require.Error(t, err)
	assert.Equal(t, io.EOF, err)
}
