package jsonrpc2

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
)

var (
	ErrHTTPEmptyResponse = errors.New("http: empty response body")
	ErrHTTPResponse      = errors.New("http: response error")
	ErrHTTPNoJSON        = errors.New("http: response did not contain application/json")
)

// HTTPBridge implements an [Encoder] and [Decoder] over a [http.Client].
//
// HTTPBridge is NOT go-routine safe, but is safe for use with [*Client] or
// [*ClientPool] which provide synchronization.
type HTTPBridge struct {
	client     *http.Client
	url        string
	respStatus string
	respBuffer bytes.Buffer
	respCode   int
	closed     bool
	respJSON   bool
}

// NewHTTPBridge builds a new [*HTTPBridge] that sends requests to url.
func NewHTTPBridge(url string) *HTTPBridge {
	return &HTTPBridge{url: url, client: new(http.Client)}
}

// Close will close any idle connections held by the underlying client and close the bridge.
//
// Any calls to the bridge after close will immediately return [io.EOF].
func (h *HTTPBridge) Close() error {
	h.client.CloseIdleConnections()
	h.closed = true

	return nil
}

// Encode implements an [Encoder].
func (h *HTTPBridge) Encode(ctx context.Context, v any) error {
	if h.closed {
		return io.EOF
	}

	h.respBuffer.Reset()
	h.respStatus = ""
	h.respCode = 0
	h.respJSON = false

	buf, err := Marshal(v)

	if err != nil {
		// Propagate marshal errors instead of swallowing them
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.url, bytes.NewReader(buf))

	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := h.client.Do(req)

	if err != nil {
		return err
	}

	defer resp.Body.Close()

	// Record response for decode
	h.respCode = resp.StatusCode
	h.respStatus = resp.Status

	if resp.Header.Get("Content-Type") == "application/json" {
		h.respJSON = true

		// ReadFrom can return io.EOF which is not an error in this context
		_, err := h.respBuffer.ReadFrom(resp.Body)
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("http: failed to read response body: %w", err)
		}
	}

	// Technically we sent the request fine, so let decode figure it out
	return nil
}

// Decode implements a [Decoder].
func (h *HTTPBridge) Decode(ctx context.Context, v any) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	if h.closed {
		return io.EOF
	}

	if h.respJSON {
		if h.respBuffer.Len() > 0 {
			return Unmarshal(h.respBuffer.Bytes(), v)
		}

		return fmt.Errorf("%w (status: %s)", ErrHTTPEmptyResponse, h.respStatus)
	}

	if h.respCode >= 200 && h.respCode < 300 {
		return fmt.Errorf("%w (status: %s)", ErrHTTPNoJSON, h.respStatus)
	}

	return fmt.Errorf("%w (status: %s)", ErrHTTPResponse, h.respStatus)
}

// Unmarshal implements [Decoder].
func (h *HTTPBridge) Unmarshal(data []byte, v any) error {
	return Unmarshal(data, v)
}
