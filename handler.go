package jsonrpc2

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrMethodAlreadyExists is returned by [MethodMux.Register] and [MethodMux.RegisterFunc]
// when attempting to register a handler for a method name that is already registered.
var ErrMethodAlreadyExists = errors.New("method already exists in mux")

// Handler defines the interface for processing JSON-RPC 2.0 requests.
// Implementations of Handler are responsible for executing the logic associated
// with a specific RPC method or routing requests to appropriate handlers.
//
// The Handle method receives the request context and the [Request] object.
// It should return a result value (which will be marshaled to JSON) and a nil error
// on success, or a nil result and an error on failure.
//
// Result Handling:
//   - If the returned result is a [*Response] or [RawResponse], it is used directly.
//   - Otherwise, the result is wrapped in a standard [Response] object.
//   - If the result is nil, it will be marshaled as JSON `null`.
//
// Error Handling:
//   - If the returned error is, or wraps, a [Error], that [Error] is used directly.
//   - Otherwise, the error is wrapped in a standard [ErrInternalError], and its
//     Error() string becomes the "data" field of the JSON-RPC error object.
//
// Notifications:
//   - If the incoming [Request] is a notification ([Request.IsNotification] is true),
//     both the result and error returned by Handle are ignored by the server.
type Handler interface {
	Handle(ctx context.Context, req *Request) (result any, err error)
}

// NewFuncHandler wraps a function `f` to create a [Handler].
// This allows using simple functions as JSON-RPC handlers without needing to define a struct type.
//
// Example:
//
//	pingHandler := jsonrpc2.NewFuncHandler(func(ctx context.Context, req *jsonrpc2.Request) (any, error) {
//	    if req.Method == "ping" {
//	        return "pong", nil
//	    }
//	    return nil, jsonrpc2.ErrMethodNotFound
//	})
//	// This pingHandler can now be used with a server or MethodMux.
//
//nolint:ireturn // Helper function intentionally returns the interface type.
func NewFuncHandler(f func(context.Context, *Request) (any, error)) Handler {
	return &funcHandler{funcHandle: f}
}

// funcHandler adapts a function to the Handler interface.
type funcHandler struct {
	funcHandle func(context.Context, *Request) (any, error)
}

// Handle calls the wrapped function.
func (fh *funcHandler) Handle(ctx context.Context, req *Request) (any, error) {
	return fh.funcHandle(ctx, req)
}

// MethodMux routes incoming requests to different [Handler] implementations
// based on the method name specified in the [Request].
// Method names are case-sensitive.
//
// A MethodMux itself implements the [Handler] interface, allowing it to be used
// directly as the main handler for an [RPCServer] or nested within other muxes.
//
// MethodMux is safe for concurrent use by multiple goroutines. Reads (Handle) and
// writes (Register, RegisterFunc) can happen concurrently, although frequent
// registration contention might impact performance. Registration should ideally
// happen during application initialization.
type MethodMux struct {
	mux sync.Map // Stores map[string]Handler
}

// NewMethodMux creates and initializes a new [*MethodMux].
func NewMethodMux() *MethodMux {
	return &MethodMux{} // sync.Map is ready to use
}

// Register associates a [Handler] with a specific method name.
// If a handler is already registered for the given method, it returns [ErrMethodAlreadyExists].
// Method names are case-sensitive.
//
// Example:
//
//	mux := jsonrpc2.NewMethodMux()
//	err := mux.Register("arith.add", addHandler) // addHandler implements jsonrpc2.Handler
//	if err != nil {
//	    // Handle registration error (e.g., duplicate method)
//	}
func (mm *MethodMux) Register(method string, handler Handler) error {
	// LoadOrStore atomically loads or stores the handler.
	// It returns the existing value if the key was already present.
	_, loaded := mm.mux.LoadOrStore(method, handler)
	if loaded {
		// The key already existed, return an error.
		return fmt.Errorf("method '%s': %w", method, ErrMethodAlreadyExists)
	}
	// The handler was successfully stored.
	return nil
}

// RegisterFunc associates a function `f` with a specific method name.
// It wraps the function using [NewFuncHandler].
// If a handler is already registered for the given method, it returns [ErrMethodAlreadyExists].
// Method names are case-sensitive.
//
// Example:
//
//	mux := jsonrpc2.NewMethodMux()
//	err := mux.RegisterFunc("utils.echo", func(ctx context.Context, req *jsonrpc2.Request) (any, error) {
//	    var params []any
//	    if err := req.Params.Unmarshal(&params); err != nil {
//	        return nil, jsonrpc2.ErrInvalidParams.WithData(err.Error())
//	    }
//	    return params, nil
//	})
//	if err != nil {
//	    // Handle registration error
//	}
func (mm *MethodMux) RegisterFunc(method string, f func(context.Context, *Request) (any, error)) error {
	return mm.Register(method, NewFuncHandler(f))
}

// Handle implements the [Handler] interface for MethodMux.
// It looks up the handler registered for the method specified in the [Request].
// If a handler is found, Handle delegates the request processing to that handler.
// If no handler is registered for the method, it returns a nil result and [ErrMethodNotFound].
func (mm *MethodMux) Handle(ctx context.Context, req *Request) (any, error) {
	// Load the handler associated with the method name.
	value, ok := mm.mux.Load(req.Method)
	if !ok {
		// Method not found.
		return nil, ErrMethodNotFound
	}

	//nolint:errcheck //Should be impossible, but handlRequest will catch the panic anyways for us
	return value.(Handler).Handle(ctx, req)
}
