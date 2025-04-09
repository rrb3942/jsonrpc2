package jsonrpc2

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
)

// HTTPHandler provides an example implementation of a jsonrpc2 as an [http.Handler]
//
// Binder may be set before the first use of the handler.
//
// HTTPHandler will set the context key of [CtxHTTPRequest] with the current [*http.Request].
type HTTPHandler struct {
	handler    Handler
	Binder     Binder
	NewEncoder NewEncoderFunc
	NewDecoder NewDecoderFunc
	MaxBytes   int64
}

// NewHTTPHandler returns an [*HTTPHandler] configured with the given handler.
func NewHTTPHandler(handler Handler) *HTTPHandler {
	return &HTTPHandler{handler: handler, NewEncoder: NewEncoder, NewDecoder: NewDecoder}
}

// ServeHTTP implements [http.Handler].
func (h *HTTPHandler) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	// Only handle json
	if req.Header.Get("Content-Type") != "application/json" {
		resp.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}

	resp.Header().Set("Content-Type", "application/json")

	body := req.Body

	if h.MaxBytes > 0 {
		body = http.MaxBytesReader(resp, body, h.MaxBytes)
	}

	// Should not write directly to ResponseWriter while reading req body (see net/http ResponseWriter docs)
	// Use intermediate buffer instead
	var buffer bytes.Buffer

	rpcServer := NewStreamServer(h.NewDecoder(body), h.NewEncoder(&buffer), h.handler)
	rpcServer.NoRoutines = true
	rpcServer.SerialBatch = true
	rpcServer.WaitOnClose = true

	sctx, stop := context.WithCancelCause(context.WithValue(req.Context(), CtxHTTPRequest, req))
	defer stop(nil)

	if h.Binder != nil {
		h.Binder.Bind(sctx, rpcServer, stop)
	}

	err := rpcServer.Run(req.Context())

	if err != nil && !errors.Is(err, io.EOF) {
		if errors.Is(err, ErrJSONTooLarge) {
			resp.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}

		resp.WriteHeader(http.StatusInternalServerError)

		return
	}

	// Flush response to client
	_, _ = buffer.WriteTo(resp)
}
