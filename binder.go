package jsonrpc2

import (
	"context"
)

// Binder provides a mechanism to configure or inspect an [RPCServer] instance
// just before its Run loop starts processing requests. This is useful for setting
// server options, callbacks, or performing context-based setup for individual
// connections or requests.
//
// When used with a [Server], the Bind method is called once for each new network
// connection accepted by the server. The provided context typically contains the
// [net.Conn] via [CtxNetConn].
//
// When used with an [HTTPHandler], the Bind method is called once per incoming
// HTTP request. The provided context typically contains the [*http.Request] via
// [CtxHTTPRequest].
//
// The `stop` function (a [context.CancelCauseFunc]) can be called within Bind
// to prematurely terminate the [RPCServer] instance before it starts processing,
// for example, based on information derived from the context (like authentication failure).
//
// When used to setup custom [Callbacks] or [Handler] the `stop` function may also be used to stop the [RPCServer]
// on specific error, or other conditions.
//
// Example Use Case: Setting a custom OnError callback based on request headers.
//
//	func myBinderFunc(ctx context.Context, rpc *jsonrpc2.RPCServer, stop context.CancelCauseFunc) {
//	    if req, ok := ctx.Value(jsonrpc2.CtxHTTPRequest).(*http.Request); ok {
//	        if req.Header.Get("X-Debug-Mode") == "true" {
//	            rpc.Callbacks.OnEncodingError = func(ctx context.Context, value any, err error) {
//			defer stop("DEBUG STOP")
//	                log.Printf("DEBUG: Encoding error for value %v: %v", value, err)
//	            }
//	        }
//	    }
//	    // Optionally, call stop(errors.New("auth failed")) to prevent Run from starting.
//	}
//	server := jsonrpc2.NewServer(myHandler)
//	server.Binder = jsonrpc2.NewFuncBinder(myBinderFunc)
type Binder interface {
	// Bind is called before the associated RPCServer starts its Run loop.
	// It receives the server's context, the RPCServer instance, and a function
	// to cancel the server's startup and run.
	Bind(ctx context.Context, rpc *RPCServer, stop context.CancelCauseFunc)
}

// NewFuncBinder wraps a function `f` to create a [Binder].
// This allows using simple functions as Binders without needing to define a struct type.
//
// Example:
//
//	logBinder := jsonrpc2.NewFuncBinder(func(ctx context.Context, rpc *jsonrpc2.RPCServer, stop context.CancelCauseFunc) {
//	    log.Printf("New RPCServer instance starting for context: %v", ctx)
//	    // Example: Add a simple exit logger
//	    rpc.Callbacks.OnExit = func(ctx context.Context, err error) {
//	        log.Printf("RPCServer exited with error: %v", err)
//	    }
//	})
//	server := jsonrpc2.NewServer(myHandler)
//	server.Binder = logBinder // Assign the binder to the server
//
//nolint:ireturn // Helper function intentionally returns the interface type.
func NewFuncBinder(f func(context.Context, *RPCServer, context.CancelCauseFunc)) Binder {
	return &funcBinder{funcBind: f}
}

// funcBinder adapts a function to the Binder interface.
type funcBinder struct {
	funcBind func(context.Context, *RPCServer, context.CancelCauseFunc)
}

// Bind implements the [Binder] interface by calling the wrapped function.
func (fb *funcBinder) Bind(ctx context.Context, rpc *RPCServer, stop context.CancelCauseFunc) {
	fb.funcBind(ctx, rpc, stop)
}
