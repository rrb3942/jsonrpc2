package jsonrpc2

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPBridge_EncodeDecode_Success(t *testing.T) {
	t.Parallel()

	reqPayload := NewRequestWithParams(int64(1), "testMethod", NewParamsArray([]string{"arg1"}))
	respPayload := NewResponseWithResult(int64(1), "resultValue")
	respBytes, err := json.Marshal(respPayload)
	assert.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		bodyBytes, rerr := io.ReadAll(r.Body)
		require.NoError(t, rerr)

		var receivedReq Request
		err = json.Unmarshal(bodyBytes, &receivedReq)
		require.NoError(t, err)
		assert.True(t, reqPayload.ID.Equal(receivedReq.ID))
		assert.Equal(t, reqPayload.Method, receivedReq.Method)
		// Simple param check for this test
		assert.Contains(t, string(bodyBytes), `"arg1"`)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(respBytes)
	}))
	defer server.Close()

	bridge := NewHTTPBridge(server.URL)
	defer bridge.Close()

	ctx := t.Context()

	// Encode the request
	err = bridge.Encode(ctx, reqPayload)
	require.NoError(t, err)

	// Decode the response
	var decodedResp Response
	err = bridge.Decode(ctx, &decodedResp)
	require.NoError(t, err)

	assert.True(t, respPayload.ID.Equal(decodedResp.ID))
	assert.False(t, decodedResp.Result.IsZero())
	assert.True(t, decodedResp.Error.IsZero())

	var result string
	err = decodedResp.Result.Unmarshal(&result)
	require.NoError(t, err)
	assert.Equal(t, "resultValue", result)
}

func TestHTTPBridge_Decode_HTTPError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Internal Server Error"))
	}))
	defer server.Close()

	bridge := NewHTTPBridge(server.URL)
	defer bridge.Close()

	ctx := t.Context()
	reqPayload := NewRequest(int64(1), "testMethod")

	// Encode (server returns error, but Encode itself shouldn't fail here)
	err := bridge.Encode(ctx, reqPayload)
	require.NoError(t, err)

	// Decode should fail with HTTPResponse error
	var decodedResp Response
	err = bridge.Decode(ctx, &decodedResp)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrHTTPResponse)
	assert.Contains(t, err.Error(), "500 Internal Server Error")
}

func TestHTTPBridge_Decode_NoJSON(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("This is not JSON"))
	}))
	defer server.Close()

	bridge := NewHTTPBridge(server.URL)
	defer bridge.Close()

	ctx := t.Context()
	reqPayload := NewRequest(int64(1), "testMethod")

	// Encode
	err := bridge.Encode(ctx, reqPayload)
	require.NoError(t, err)

	// Decode should fail with ErrHTTPNotJSON
	var decodedResp Response
	err = bridge.Decode(ctx, &decodedResp)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrHTTPNotJSON)
	assert.Contains(t, err.Error(), "200 OK")
}

func TestHTTPBridge_Decode_EmptyJSON(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// No body written
	}))
	defer server.Close()

	bridge := NewHTTPBridge(server.URL)
	defer bridge.Close()

	ctx := t.Context()
	reqPayload := NewRequest(int64(1), "testMethod")

	// Encode
	err := bridge.Encode(ctx, reqPayload)
	require.NoError(t, err)

	// Decode should fail with ErrHTTPEmptyResponse
	var decodedResp Response
	err = bridge.Decode(ctx, &decodedResp)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrHTTPEmptyResponse)
	assert.Contains(t, err.Error(), "200 OK")
}

func TestHTTPBridge_Encode_MarshalError(t *testing.T) {
	t.Parallel()

	bridge := NewHTTPBridge("http://localhost:12345") // URL doesn't matter here
	defer bridge.Close()

	ctx := t.Context()
	// Use a channel, which cannot be marshaled to JSON
	invalidPayload := make(chan int)

	err := bridge.Encode(ctx, invalidPayload)
	require.Error(t, err)

	// Decode should fail because no request was actually sent and no response received
	var decodedResp Response
	err = bridge.Decode(ctx, &decodedResp)
	require.Error(t, err)
	// It fails because respCode is 0, leading to ErrHTTPResponse
	assert.ErrorIs(t, err, ErrHTTPResponse)
	assert.Contains(t, err.Error(), "status: ") // Status is empty
}

func TestHTTPBridge_Encode_ClientError(t *testing.T) {
	t.Parallel()

	// Use a non-existent server address
	bridge := NewHTTPBridge("http://localhost:65534")
	defer bridge.Close()

	ctx := t.Context()
	reqPayload := NewRequest(int64(1), "testMethod")

	err := bridge.Encode(ctx, reqPayload)
	require.Error(t, err) // Expecting a connection error
	assert.Contains(t, err.Error(), "dial tcp")

	// Decode should also fail as no response was processed
	var decodedResp Response
	err = bridge.Decode(ctx, &decodedResp)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrHTTPResponse) // Fails because respCode is 0
}

func TestHTTPBridge_ContextCancellation_Encode(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond) // Ensure request takes time
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	bridge := NewHTTPBridge(server.URL)
	defer bridge.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond) // Short timeout
	defer cancel()

	reqPayload := NewRequest(int64(1), "testMethod")

	err := bridge.Encode(ctx, reqPayload)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestHTTPBridge_ContextCancellation_Decode(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "id": 1, "result": "ok"}`))
	}))
	defer server.Close()

	bridge := NewHTTPBridge(server.URL)
	defer bridge.Close()

	ctx := t.Context()
	reqPayload := NewRequest(int64(1), "testMethod")

	// Encode successfully
	err := bridge.Encode(ctx, reqPayload)
	require.NoError(t, err)

	// Now try to decode with a cancelled context
	ctxCancelled, cancel := context.WithCancel(t.Context())
	cancel() // Cancel immediately

	var decodedResp Response
	err = bridge.Decode(ctxCancelled, &decodedResp)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestHTTPBridge_Close(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	bridge := NewHTTPBridge(server.URL)

	// Close the bridge
	err := bridge.Close()
	require.NoError(t, err)

	ctx := t.Context()
	reqPayload := NewRequest(int64(1), "testMethod")

	// Encode should fail with EOF
	err = bridge.Encode(ctx, reqPayload)
	require.Error(t, err)
	assert.Equal(t, io.EOF, err)

	// Decode should fail with EOF
	var decodedResp Response
	err = bridge.Decode(ctx, &decodedResp)
	require.Error(t, err)
	assert.Equal(t, io.EOF, err)
}

func TestHTTPBridge_Unmarshal(t *testing.T) {
	bridge := NewHTTPBridge("http://localhost:12345") // URL doesn't matter
	defer bridge.Close()

	type testStruct struct {
		Field string `json:"field"`
	}

	jsonData := []byte(`{"field": "value"}`)

	var result testStruct

	// Use a custom unmarshaler to verify it's called
	originalUnmarshal := Unmarshal
	unmarshalCalled := false
	Unmarshal = func(data []byte, v any) error {
		unmarshalCalled = true
		// Use the original json.Unmarshal for the actual work
		return json.Unmarshal(data, v)
	}

	defer func() { Unmarshal = originalUnmarshal }() // Restore original

	err := bridge.Unmarshal(jsonData, &result)
	require.NoError(t, err)
	assert.True(t, unmarshalCalled, "Unmarshal should have been called")
	assert.Equal(t, "value", result.Field)
}

// Test that Encode handles nil input gracefully (should marshal to JSON null).
func TestHTTPBridge_Encode_Nil(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.Equal(t, "null", string(bodyBytes)) // Expect JSON null

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "id": null, "result": "ok"}`)) // Example response
	}))
	defer server.Close()

	bridge := NewHTTPBridge(server.URL)
	defer bridge.Close()

	ctx := t.Context()

	// Encode nil
	err := bridge.Encode(ctx, nil)
	require.NoError(t, err)

	// Decode the response (just check for no error)
	var decodedResp Response
	err = bridge.Decode(ctx, &decodedResp)
	require.NoError(t, err)
	assert.Equal(t, NewNullID(), decodedResp.ID) // Check if ID matches response

	var result string
	err = decodedResp.Result.Unmarshal(&result)
	require.NoError(t, err)
	assert.Equal(t, "ok", result)
}

// Test that Decode handles nil input gracefully.
func TestHTTPBridge_Decode_NilInput(t *testing.T) {
	t.Parallel()

	respPayload := NewResponseWithResult(int64(1), "resultValue")
	respBytes, err := json.Marshal(respPayload)
	assert.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(respBytes)
	}))
	defer server.Close()

	bridge := NewHTTPBridge(server.URL)
	defer bridge.Close()

	ctx := t.Context()

	// Encode something arbitrary to get a response
	err = bridge.Encode(ctx, NewRequest(int64(1), "foo"))
	require.NoError(t, err)

	// Decode into nil - should cause an Unmarshal error (json: Unmarshal(nil *<nil>))
	err = bridge.Decode(ctx, nil)
	require.Error(t, err)

	var unmarshalTypeError *json.InvalidUnmarshalError
	ok := errors.As(err, &unmarshalTypeError)
	require.True(t, ok, "Expected a json.InvalidUnmarshalError")
}

// Test that Decode handles invalid JSON in the response body.
func TestHTTPBridge_Decode_InvalidJSONResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "id": 1, "result": "ok`)) // Incomplete JSON
	}))
	defer server.Close()

	bridge := NewHTTPBridge(server.URL)
	defer bridge.Close()

	ctx := t.Context()
	reqPayload := NewRequest(int64(1), "testMethod")

	// Encode
	err := bridge.Encode(ctx, reqPayload)
	require.NoError(t, err)

	// Decode should fail with a JSON syntax error
	var decodedResp Response
	err = bridge.Decode(ctx, &decodedResp)
	require.Error(t, err)

	var syntaxError *json.SyntaxError
	ok := errors.As(err, &syntaxError)
	require.True(t, ok, "Expected a json.SyntaxError")
}

// Test that Encode handles invalid URL format during request creation.
func TestHTTPBridge_Encode_InvalidURL(t *testing.T) {
	t.Parallel()

	// Provide an invalid URL with control characters
	invalidURL := "http://localhost:\badport"

	bridge := NewHTTPBridge(invalidURL)
	defer bridge.Close()

	ctx := t.Context()
	reqPayload := NewRequest(int64(1), "testMethod")

	// Encode should fail during http.NewRequestWithContext
	err := bridge.Encode(ctx, reqPayload)
	require.Error(t, err)
	// Check if the error is related to URL parsing
	assert.True(t, strings.Contains(err.Error(), "invalid control character in URL") || strings.Contains(err.Error(), "invalid port"), "Expected URL parsing error")
}

// Helper type for testing Marshal error in Encode.
type marshalErrorType struct{}

func (m *marshalErrorType) MarshalJSON() ([]byte, error) {
	return nil, errors.New("marshal error")
}

// Test Encode specifically for Marshal errors (revisiting TestHTTPBridge_Encode_MarshalError).
func TestHTTPBridge_Encode_MarshalError_Specific(t *testing.T) {
	t.Parallel()

	bridge := NewHTTPBridge("http://localhost:12345") // URL doesn't matter here
	defer bridge.Close()

	ctx := t.Context()
	invalidPayload := &marshalErrorType{}

	// Encode should return the marshal error directly now
	err := bridge.Encode(ctx, invalidPayload)
	require.Error(t, err)
	assert.ErrorContains(t, err, "marshal error")

	// Decode should fail because no request was sent
	var decodedResp Response
	err = bridge.Decode(ctx, &decodedResp)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrHTTPResponse) // Fails because respCode is 0
}

// Test Decode when context is already cancelled before calling.
func TestHTTPBridge_Decode_ContextAlreadyCancelled(t *testing.T) {
	t.Parallel()

	bridge := NewHTTPBridge("http://localhost:12345") // URL doesn't matter
	defer bridge.Close()

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // Cancel immediately

	var decodedResp Response
	err := bridge.Decode(ctx, &decodedResp)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}
