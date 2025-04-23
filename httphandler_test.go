package jsonrpc2

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testHandler is a simple handler for testing purposes.
func testHandler(ctx context.Context, req *Request) (any, error) {
	switch req.Method {
	case "echo":
		var params []any
		if err := req.Params.Unmarshal(&params); err != nil {
			return nil, ErrInvalidParams.WithData(err.Error())
		}
		return params, nil
	case "ping":
		return "pong", nil
	case "error":
		return nil, NewError(123, "test error")
	case "notify":
		// No response for notifications
		return nil, nil
	case "checkContext":
		httpReq, ok := ctx.Value(CtxHTTPRequest).(*http.Request)
		if !ok {
			return "context key not found", nil
		}
		return httpReq.Method, nil
	default:
		return nil, ErrMethodNotFound
	}
}

func TestNewHTTPHandler(t *testing.T) {
	h := NewHTTPHandler(MethodMux{"test": Func(testHandler)})
	require.NotNil(t, h)
	assert.NotNil(t, h.handler)
	assert.NotNil(t, h.NewEncoder) // Should default to jsonrpc2.NewEncoder
	assert.NotNil(t, h.NewDecoder) // Should default to jsonrpc2.NewDecoder
	assert.Zero(t, h.MaxBytes)
}

func TestHTTPHandler_ServeHTTP_SimpleRequest(t *testing.T) {
	handler := NewHTTPHandler(Func(testHandler))
	server := httptest.NewServer(handler)
	defer server.Close()

	reqBody := `{"jsonrpc": "2.0", "method": "ping", "id": 1}`
	resp, err := http.Post(server.URL, "application/json", strings.NewReader(reqBody))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var rpcResp Response
	err = json.Unmarshal(bodyBytes, &rpcResp)
	require.NoError(t, err)

	assert.Equal(t, Version, rpcResp.Jsonrpc)
	assert.Equal(t, json.Number("1"), rpcResp.ID.NumberOrZero())
	assert.True(t, rpcResp.Error.IsZero())
	require.False(t, rpcResp.Result.IsZero())

	var result string
	err = rpcResp.Result.Unmarshal(&result)
	require.NoError(t, err)
	assert.Equal(t, "pong", result)
}

func TestHTTPHandler_ServeHTTP_BatchRequest(t *testing.T) {
	handler := NewHTTPHandler(Func(testHandler))
	server := httptest.NewServer(handler)
	defer server.Close()

	reqBody := `[
		{"jsonrpc": "2.0", "method": "ping", "id": 1},
		{"jsonrpc": "2.0", "method": "echo", "params": ["hello"], "id": 2}
	]`
	resp, err := http.Post(server.URL, "application/json", strings.NewReader(reqBody))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var rpcResps []Response
	err = json.Unmarshal(bodyBytes, &rpcResps)
	require.NoError(t, err)
	require.Len(t, rpcResps, 2)

	// Response order might not be guaranteed, check both
	foundPing := false
	foundEcho := false
	for _, rpcResp := range rpcResps {
		assert.Equal(t, Version, rpcResp.Jsonrpc)
		assert.True(t, rpcResp.Error.IsZero())
		require.False(t, rpcResp.Result.IsZero())

		idNum, _ := rpcResp.ID.Number()
		switch idNum {
		case "1": // ping response
			var result string
			err = rpcResp.Result.Unmarshal(&result)
			require.NoError(t, err)
			assert.Equal(t, "pong", result)
			foundPing = true
		case "2": // echo response
			var result []string
			err = rpcResp.Result.Unmarshal(&result)
			require.NoError(t, err)
			assert.Equal(t, []string{"hello"}, result)
			foundEcho = true
		default:
			t.Fatalf("Unexpected response ID: %v", rpcResp.ID)
		}
	}
	assert.True(t, foundPing, "Did not find ping response")
	assert.True(t, foundEcho, "Did not find echo response")
}

func TestHTTPHandler_ServeHTTP_Notification(t *testing.T) {
	handler := NewHTTPHandler(Func(testHandler))
	server := httptest.NewServer(handler)
	defer server.Close()

	reqBody := `{"jsonrpc": "2.0", "method": "notify"}` // No ID means notification
	resp, err := http.Post(server.URL, "application/json", strings.NewReader(reqBody))
	require.NoError(t, err)
	defer resp.Body.Close()

	// Notifications should result in No Content
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Empty(t, bodyBytes)
}

func TestHTTPHandler_ServeHTTP_InvalidContentType(t *testing.T) {
	handler := NewHTTPHandler(Func(testHandler))
	server := httptest.NewServer(handler)
	defer server.Close()

	reqBody := `{"jsonrpc": "2.0", "method": "ping", "id": 1}`
	resp, err := http.Post(server.URL, "text/plain", strings.NewReader(reqBody)) // Invalid Content-Type
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnsupportedMediaType, resp.StatusCode)
}

func TestHTTPHandler_ServeHTTP_InvalidJSON(t *testing.T) {
	handler := NewHTTPHandler(Func(testHandler))
	server := httptest.NewServer(handler)
	defer server.Close()

	reqBody := `{"jsonrpc": "2.0", "method": "ping", "id": 1` // Missing closing brace
	resp, err := http.Post(server.URL, "application/json", strings.NewReader(reqBody))
	require.NoError(t, err)
	defer resp.Body.Close()

	// The server should still try to process and return a JSON-RPC error
	assert.Equal(t, http.StatusOK, resp.StatusCode) // Or potentially InternalServerError depending on exact failure point
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var rpcResp Response
	err = json.Unmarshal(bodyBytes, &rpcResp)
	require.NoError(t, err)

	assert.False(t, rpcResp.Error.IsZero())
	assert.Equal(t, ErrParseError.Code(), rpcResp.Error.Code()) // Or InvalidRequest
}

func TestHTTPHandler_ServeHTTP_HandlerError(t *testing.T) {
	handler := NewHTTPHandler(Func(testHandler))
	server := httptest.NewServer(handler)
	defer server.Close()

	reqBody := `{"jsonrpc": "2.0", "method": "error", "id": 1}`
	resp, err := http.Post(server.URL, "application/json", strings.NewReader(reqBody))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var rpcResp Response
	err = json.Unmarshal(bodyBytes, &rpcResp)
	require.NoError(t, err)

	assert.Equal(t, json.Number("1"), rpcResp.ID.NumberOrZero())
	assert.True(t, rpcResp.Result.IsZero())
	require.False(t, rpcResp.Error.IsZero())
	assert.Equal(t, int64(123), rpcResp.Error.Code())
	assert.Equal(t, "test error", rpcResp.Error.Error())
}

func TestHTTPHandler_ServeHTTP_MaxBytes(t *testing.T) {
	handler := NewHTTPHandler(Func(testHandler))
	handler.MaxBytes = 10 // Set a small limit
	server := httptest.NewServer(handler)
	defer server.Close()

	reqBody := `{"jsonrpc": "2.0", "method": "ping", "id": 1}` // This is larger than 10 bytes
	resp, err := http.Post(server.URL, "application/json", strings.NewReader(reqBody))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
}

func TestHTTPHandler_ServeHTTP_Binder(t *testing.T) {
	var boundServer *RPCServer
	var boundCtx context.Context
	binder := func(ctx context.Context, server *RPCServer, stop context.CancelCauseFunc) {
		boundServer = server
		boundCtx = ctx
		// Example: Add a specific callback via binder
		server.Callbacks.OnDecodingError = func(ctx context.Context, m json.RawMessage, e error) {
			t.Logf("Binder OnDecodingError called: %v", e)
		}
	}

	handler := NewHTTPHandler(Func(testHandler))
	handler.Binder = BinderFunc(binder) // Use BinderFunc adapter
	server := httptest.NewServer(handler)
	defer server.Close()

	// 1. Test successful request to ensure binder was called
	reqBody := `{"jsonrpc": "2.0", "method": "ping", "id": 1}`
	resp, err := http.Post(server.URL, "application/json", strings.NewReader(reqBody))
	require.NoError(t, err)
	resp.Body.Close() // Close immediately, just checking status and binder call
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotNil(t, boundServer, "Binder was not called")
	require.NotNil(t, boundCtx, "Binder was not called")
	assert.NotNil(t, boundServer.Callbacks.OnDecodingError, "Callback set by binder is nil")

	// Check context value set by HTTPHandler
	httpReqFromCtx, ok := boundCtx.Value(CtxHTTPRequest).(*http.Request)
	require.True(t, ok, "CtxHTTPRequest not found in context passed to Binder")
	assert.Equal(t, http.MethodPost, httpReqFromCtx.Method) // Check if it's the correct request

	// 2. Test if binder-set callback works (trigger decoding error)
	reqBodyInvalid := `{"jsonrpc": "2.0", `
	resp, err = http.Post(server.URL, "application/json", strings.NewReader(reqBodyInvalid))
	require.NoError(t, err)
	defer resp.Body.Close()
	// Expecting OK status because the server handles the error and returns a JSON-RPC error response
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	// We can't easily assert that the specific log line appeared without more complex test setup,
	// but we know the callback was set. If the test passes, it implies the binder logic ran.
}

func TestHTTPHandler_ServeHTTP_ContextPropagation(t *testing.T) {
	handler := NewHTTPHandler(Func(testHandler))
	server := httptest.NewServer(handler)
	defer server.Close()

	reqBody := `{"jsonrpc": "2.0", "method": "checkContext", "id": 1}`
	resp, err := http.Post(server.URL, "application/json", strings.NewReader(reqBody))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var rpcResp Response
	err = json.Unmarshal(bodyBytes, &rpcResp)
	require.NoError(t, err)

	assert.True(t, rpcResp.Error.IsZero())
	var result string
	err = rpcResp.Result.Unmarshal(&result)
	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, result) // Handler should return the HTTP method from context
}

// BinderFunc is an adapter to allow the use of ordinary functions as Binders.
type BinderFunc func(context.Context, *RPCServer, context.CancelCauseFunc)

// Bind calls f(ctx, server, stop).
func (f BinderFunc) Bind(ctx context.Context, server *RPCServer, stop context.CancelCauseFunc) {
	f(ctx, server, stop)
}

// Helper to create a request and recorder for direct handler testing
func executeRequest(t *testing.T, h http.Handler, method, path, contentType string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestHTTPHandler_ServeHTTP_DirectCall(t *testing.T) {
	handler := NewHTTPHandler(Func(testHandler))

	reqBody := `{"jsonrpc": "2.0", "method": "ping", "id": 1}`
	rr := executeRequest(t, handler, http.MethodPost, "/", "application/json", strings.NewReader(reqBody))

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

	var rpcResp Response
	err := json.Unmarshal(rr.Body.Bytes(), &rpcResp)
	require.NoError(t, err)

	assert.Equal(t, Version, rpcResp.Jsonrpc)
	assert.Equal(t, json.Number("1"), rpcResp.ID.NumberOrZero())
	assert.True(t, rpcResp.Error.IsZero())
	var result string
	err = rpcResp.Result.Unmarshal(&result)
	require.NoError(t, err)
	assert.Equal(t, "pong", result)
}

func TestHTTPHandler_ServeHTTP_NoBodyResponse(t *testing.T) {
	// Handler that returns nothing successfully (like a notification handler, but with an ID)
	nullHandler := func(ctx context.Context, req *Request) (any, error) {
		// Simulate successful processing but no result data to return
		// Note: Returning `nil, nil` for a request with an ID technically violates
		// JSON-RPC spec (should have `result: null`), but we test how the HTTP handler deals with it.
		// A more correct handler would return `NewResult(nil), nil`
		return nil, nil
	}

	handler := NewHTTPHandler(Func(nullHandler))
	server := httptest.NewServer(handler)
	defer server.Close()

	reqBody := `{"jsonrpc": "2.0", "method": "anything", "id": 1}`
	resp, err := http.Post(server.URL, "application/json", strings.NewReader(reqBody))
	require.NoError(t, err)
	defer resp.Body.Close()

	// Even if the handler returns nil result, a valid JSON-RPC response with "result": null should be formed.
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	// Expect `{"jsonrpc":"2.0","result":null,"id":1}`
	var rpcResp Response
	err = json.Unmarshal(bodyBytes, &rpcResp)
	require.NoError(t, err, "Response body: %s", string(bodyBytes))

	assert.Equal(t, Version, rpcResp.Jsonrpc)
	assert.Equal(t, json.Number("1"), rpcResp.ID.NumberOrZero())
	assert.True(t, rpcResp.Error.IsZero())
	// Check that result is explicitly null
	assert.Equal(t, `null`, string(rpcResp.Result.RawMessage()))
}

func TestHTTPHandler_ServeHTTP_EmptyBatchResponse(t *testing.T) {
	// Handler that handles notifications only
	notifyOnlyHandler := func(ctx context.Context, req *Request) (any, error) {
		if req.IsNotification() {
			// Process notification
			return nil, nil // No response for notifications
		}
		return nil, ErrMethodNotFound // Error for non-notifications
	}

	handler := NewHTTPHandler(Func(notifyOnlyHandler))
	server := httptest.NewServer(handler)
	defer server.Close()

	// Batch containing only notifications
	reqBody := `[
		{"jsonrpc": "2.0", "method": "notify1"},
		{"jsonrpc": "2.0", "method": "notify2"}
	]`
	resp, err := http.Post(server.URL, "application/json", strings.NewReader(reqBody))
	require.NoError(t, err)
	defer resp.Body.Close()

	// If a batch request contains only notifications, the server MUST NOT return a response.
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Empty(t, bodyBytes)
}

func TestHTTPHandler_ServeHTTP_MixedBatchResponse(t *testing.T) {
	handler := NewHTTPHandler(Func(testHandler)) // Use standard test handler
	server := httptest.NewServer(handler)
	defer server.Close()

	// Batch containing a request and a notification
	reqBody := `[
		{"jsonrpc": "2.0", "method": "ping", "id": 123},
		{"jsonrpc": "2.0", "method": "notify"}
	]`
	resp, err := http.Post(server.URL, "application/json", strings.NewReader(reqBody))
	require.NoError(t, err)
	defer resp.Body.Close()

	// Response should contain only the response for the non-notification request
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	// Expecting a JSON array with a single response object
	var rpcResps []Response
	err = json.Unmarshal(bodyBytes, &rpcResps)
	require.NoError(t, err, "Failed to unmarshal response: %s", string(bodyBytes))
	require.Len(t, rpcResps, 1)

	// Check the single response object
	rpcResp := rpcResps[0]
	assert.Equal(t, Version, rpcResp.Jsonrpc)
	assert.Equal(t, json.Number("123"), rpcResp.ID.NumberOrZero())
	assert.True(t, rpcResp.Error.IsZero())
	var result string
	err = rpcResp.Result.Unmarshal(&result)
	require.NoError(t, err)
	assert.Equal(t, "pong", result)
}

// Custom encoder/decoder for testing injection
type customEncoder struct {
	Encoder
	called bool
}

func (c *customEncoder) Encode(ctx context.Context, v any) error {
	c.called = true
	return c.Encoder.Encode(ctx, v)
}

type customDecoder struct {
	Decoder
	called bool
}

func (c *customDecoder) Decode(ctx context.Context, v any) error {
	c.called = true
	return c.Decoder.Decode(ctx, v)
}

func TestHTTPHandler_ServeHTTP_CustomEncoderDecoder(t *testing.T) {
	var enc *customEncoder
	var dec *customDecoder

	newEnc := func(w io.Writer) Encoder {
		enc = &customEncoder{Encoder: NewEncoder(w)}
		return enc
	}
	newDec := func(r io.Reader) Decoder {
		dec = &customDecoder{Decoder: NewDecoder(r)}
		return dec
	}

	handler := NewHTTPHandler(Func(testHandler))
	handler.NewEncoder = newEnc
	handler.NewDecoder = newDec

	server := httptest.NewServer(handler)
	defer server.Close()

	reqBody := `{"jsonrpc": "2.0", "method": "ping", "id": 1}`
	resp, err := http.Post(server.URL, "application/json", strings.NewReader(reqBody))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotNil(t, enc, "Custom encoder func not called")
	require.NotNil(t, dec, "Custom decoder func not called")
	assert.True(t, enc.called, "Custom encoder Encode not called")
	// Note: Decode might be called multiple times depending on internal buffering or batch handling
	assert.True(t, dec.called, "Custom decoder Decode not called")

	// Verify response is still correct
	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var rpcResp Response
	err = json.Unmarshal(bodyBytes, &rpcResp)
	require.NoError(t, err)
	assert.Equal(t, json.Number("1"), rpcResp.ID.NumberOrZero())
	var result string
	err = rpcResp.Result.Unmarshal(&result)
	require.NoError(t, err)
	assert.Equal(t, "pong", result)
}
