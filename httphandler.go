package jsonrpc2

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
)

var (
	// errMaxBytes is used for type assertion to check for http.MaxBytesError.
	errMaxBytes = &http.MaxBytesError{}
)

// HTTPHandler adapts a jsonrpc2 [Handler] to the standard [net/http.Handler] interface.
// It allows serving JSON-RPC 2.0 requests over HTTP.
//
// Create instances using [NewHTTPHandler]. After creation, you can optionally configure:
//   - Binder: A [Binder] implementation to manage server lifecycle or context.
//   - NewEncoder/NewDecoder: Custom functions for creating JSON encoders/decoders.
//     Defaults to the package-level [NewEncoder] and [NewDecoder].
//   - MaxBytes: A limit on the request body size (in bytes). Defaults to 0 (no limit).
//
// During request processing, HTTPHandler automatically:
//   - Validates the "Content-Type" header is "application/json".
//   - Sets the "Content-Type" header of the response to "application/json".
//   - Injects the [*http.Request] into the request context using the [CtxHTTPRequest] key.
//   - Handles request size limits if MaxBytes is set.
//   - Manages an internal [RPCServer] configured for synchronous, serial processing suitable for HTTP.
//   - Translates specific errors (like size limits) into appropriate HTTP status codes.
type HTTPHandler struct {
	handler Handler // The core JSON-RPC request handler.

	// Binder allows optional binding of the RPC server lifecycle or context.
	// Can be set after creating the handler with NewHTTPHandler.
	Binder Binder

	// NewEncoder specifies the function to create a JSON encoder for responses.
	// Defaults to the package-level jsonrpc2.NewEncoder.
	// Can be set after creating the handler with NewHTTPHandler.
	NewEncoder NewEncoderFunc

	// NewDecoder specifies the function to create a JSON decoder for requests.
	// Defaults to the package-level jsonrpc2.NewDecoder.
	// Can be set after creating the handler with NewHTTPHandler.
	NewDecoder NewDecoderFunc

	// MaxBytes specifies the maximum number of bytes to read from the request body.
	// If 0 or less, no limit is enforced by this handler (http.MaxBytesReader is not used).
	// Can be set after creating the handler with NewHTTPHandler.
	MaxBytes int64
}

// NewHTTPHandler creates a new [*HTTPHandler] that wraps the provided jsonrpc2 [Handler].
// It uses the default package-level [NewEncoder] and [NewDecoder] functions.
//
// Example:
//
//	mux := jsonrpc2.NewMethodMux()
//	mux.RegisterFunc("echo", func(ctx context.Context, req *jsonrpc2.Request) (any, error) {
//	    return req.Params, nil
//	})
//	httpHandler := jsonrpc2.NewHTTPHandler(mux)
//	http.Handle("/rpc", httpHandler)
//	log.Fatal(http.ListenAndServe(":8080", nil))
func NewHTTPHandler(handler Handler) *HTTPHandler {
	return &HTTPHandler{handler: handler, NewEncoder: NewEncoder, NewDecoder: NewDecoder}
}

// ServeHTTP processes an incoming HTTP request, expecting it to be a JSON-RPC 2.0 call.
// It implements the [net/http.Handler] interface.
//
// The method performs the following steps:
//  1. Checks if the request "Content-Type" is "application/json". Responds with 415 Unsupported Media Type if not.
//  2. Sets the response "Content-Type" to "application/json".
//  3. If MaxBytes > 0, wraps the request body with http.MaxBytesReader.
//  4. Creates an internal, short-lived [RPCServer] configured with the handler's NewDecoder, NewEncoder, and Handler.
//     This server runs synchronously (NoRoutines=true, SerialBatch=true) suitable for a single HTTP request/response cycle.
//  5. Creates a new context derived from the request context, adding the *http.Request using the CtxHTTPRequest key.
//  6. If a Binder is configured, calls its Bind method.
//  7. Runs the internal RPCServer to process the request body.
//  8. Handles potential errors:
//     - EOF is ignored (normal end of request).
//     - UnexpectedEOF (often malformed JSON) results in a JSON-RPC Parse Error response.
//     - MaxBytesError results in a 413 Request Entity Too Large response.
//     - Other errors result in a 500 Internal Server Error response.
//  9. Writes the JSON-RPC response (if any) from the internal buffer to the http.ResponseWriter.
//  10. If no response was generated (e.g., for Notifications), responds with 204 No Content.
func (h *HTTPHandler) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	// 1. Check Content-Type
	if req.Header.Get("Content-Type") != "application/json" {
		resp.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}

	// 2. Set Response Content-Type
	resp.Header().Set("Content-Type", "application/json")

	body := req.Body
	// 3. Apply MaxBytes limit
	if h.MaxBytes > 0 {
		body = http.MaxBytesReader(resp, body, h.MaxBytes)
	}

	// Use an intermediate buffer for the response. Writing directly to resp
	// while potentially still reading req.Body can cause issues.
	var buffer bytes.Buffer

	// 4. Create internal RPCServer
	// Configure for synchronous execution suitable for HTTP request handling.
	rpcServer := NewStreamServer(h.NewDecoder(body), h.NewEncoder(&buffer), h.handler)
	rpcServer.NoRoutines = true  // Handle requests sequentially in the current goroutine.
	rpcServer.SerialBatch = true // Process batch requests serially.
	rpcServer.WaitOnClose = true // Ensure Run waits for processing to complete.

	// 5. Create context with HTTP request
	sctx, stop := context.WithCancelCause(context.WithValue(req.Context(), CtxHTTPRequest, req))
	defer stop(nil) // Ensure context cancellation is signaled on exit.

	// 6. Call Binder if configured
	if h.Binder != nil {
		// The binder might further configure the server or manage its lifecycle
		// within the scope of this request.
		h.Binder.Bind(sctx, rpcServer, stop)
	}

	// 7. Run the RPC server to process the request body
	err := rpcServer.Run(sctx)

	// 8. Handle errors
	// Ignore io.EOF which signifies a clean end of the request stream.
	if err != nil && !errors.Is(err, io.EOF) {
		switch {
		// Check for maxbytes first, as it could also trigger UnexpectedEOF
		case errors.As(err, &errMaxBytes):
			// Request body exceeded the MaxBytes limit.
			resp.WriteHeader(http.StatusRequestEntityTooLarge)
			return // Stop processing, header already sent.
		case errors.Is(err, io.ErrUnexpectedEOF):
			// We may have some writes to buffer here, but those writes are for fully parsed objects.
			// It is unclear if we should drop those responses or respond with what we have plus an
			// additional error. For now, respond with what we have and an additional error.
			// Marshal and write the standard Parse Error response. Ignore marshalling errors here.
			if buf, merr := Marshal(NewResponseError(ErrParseError)); merr == nil {
				_, _ = buffer.Write(buf) // Write error response to the buffer
			}
			// Proceed to write the buffer content (the error response) below.
		default:
			// For other unexpected errors during RPC processing.
			resp.WriteHeader(http.StatusInternalServerError)
			return // Stop processing, header already sent.
		}
	}

	// 9. Write response from buffer
	if buffer.Len() > 0 {
		// Flush the buffered response (either successful result or Parse Error) to the client.
		_, _ = buffer.WriteTo(resp)
	} else {
		// 10. Handle No Content (e.g., for notifications or successful batches with no responses)
		resp.WriteHeader(http.StatusNoContent)
	}
}
