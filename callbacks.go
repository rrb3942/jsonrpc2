package jsonrpc2

import (
	"context"
	"encoding/json"
	"log/slog"
)

// DefaultOnHandlerPanic provides a basic panic logging mechanism using the standard `slog` package.
// It is assigned to [Callbacks.OnHandlerPanic] by default when an [RPCServer] is created,
// ensuring that handler panics are logged even if no custom callbacks are configured.
var DefaultOnHandlerPanic = func(ctx context.Context, req *Request, rec any) {
	// Logs the error with relevant context.
	slog.ErrorContext(ctx, "Panic recovered in JSON-RPC handler", "method", req.Method, "id", req.ID.Value(), "params", string(req.Params.RawMessage()), "panic_value", rec)
}

// Callbacks defines a set of functions that can be registered with an [RPCServer]
// to be executed upon specific events during the server's lifecycle.
// This allows for custom logging, error handling, or other actions.
//
// Callbacks are typically assigned to an [RPCServer] instance *before* its [RPCServer.Run]
// method is called, often using a [Binder]. Callbacks should be safe for concurrent use
// if the server processes requests concurrently (i.e., [RPCServer.NoRoutines] is false).
//
// Callbacks should *not* modify the [RPCServer] state directly.
//
// Example (using a Binder to set callbacks):
//
//	func setupCallbacks(ctx context.Context, rpc *jsonrpc2.RPCServer, stop context.CancelCauseFunc) {
//	    rpc.Callbacks.OnExit = func(ctx context.Context, err error) {
//	        slog.InfoContext(ctx, "RPCServer exiting", "error", err)
//	    }
//	    rpc.Callbacks.OnHandlerPanic = func(ctx context.Context, req *jsonrpc2.Request, rec any) {
//	        slog.ErrorContext(ctx, "Custom panic handler", "method", req.Method, "panic", rec)
//	        // Maybe trigger monitoring alert here
//	    }
//	    // Assign other callbacks as needed...
//	}
//
//	server := jsonrpc2.NewServer(myHandler) // Assuming myHandler is defined
//	server.Binder = jsonrpc2.NewFuncBinder(setupCallbacks)
//	// Now run the server... server.ListenAndServe(...)
type Callbacks struct {
	// OnExit is called when the [RPCServer.Run] method is about to return.
	// The `err` parameter indicates the reason for exiting, which might be nil
	// on graceful shutdown, or an error like [io.EOF], [context.Canceled],
	// or a connection error.
	OnExit func(ctx context.Context, err error)

	// OnDecodingError is called when the server fails to decode an incoming message
	// into a valid JSON-RPC Request or Batch structure.
	// The `raw` parameter contains the raw JSON message that failed decoding (it might
	// be incomplete or nil if the error occurred before reading any data).
	// The `err` parameter provides details about the decoding failure (e.g.,
	// JSON syntax error, invalid structure).
	OnDecodingError func(ctx context.Context, raw json.RawMessage, err error)

	// OnEncodingError is called when the server fails to encode a response object
	// before sending it back to the client.
	// The `value` parameter is the response object ([*Response] or `[]any` for batches)
	// that failed to encode.
	// The `err` parameter provides details about the encoding failure. This might
	// indicate issues like unsupported types within the response data.
	OnEncodingError func(ctx context.Context, value any, err error)

	// OnHandlerPanic is called when a panic occurs within the user-provided [Handler]
	// during request processing. The panic is recovered by the server.
	// The `req` parameter is the [*Request] that caused the panic.
	// The `rec` parameter is the value recovered from the panic.
	// The default behavior is handled by [DefaultOnHandlerPanic]. Assigning a custom
	// function here overrides the default.
	OnHandlerPanic func(ctx context.Context, req *Request, rec any)
}

// runOnExit calls the OnExit callback if it is set.
func (c *Callbacks) runOnExit(ctx context.Context, e error) {
	if c.OnExit != nil {
		c.OnExit(ctx, e)
	}
}

// runOnDecodingError calls the OnDecodingError callback if it is set.
func (c *Callbacks) runOnDecodingError(ctx context.Context, m json.RawMessage, e error) {
	if c.OnDecodingError != nil {
		c.OnDecodingError(ctx, m, e)
	}
}

// runOnEncodingError calls the OnEncodingError callback if it is set.
func (c *Callbacks) runOnEncodingError(ctx context.Context, d any, e error) {
	if c.OnEncodingError != nil {
		c.OnEncodingError(ctx, d, e)
	}
}

// runOnHandlerPanic calls the OnHandlerPanic callback if it is set.
func (c *Callbacks) runOnHandlerPanic(ctx context.Context, r *Request, recovery any) {
	if c.OnHandlerPanic != nil {
		c.OnHandlerPanic(ctx, r, recovery)
	}
}
