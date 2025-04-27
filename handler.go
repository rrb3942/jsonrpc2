package jsonrpc2

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
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

// MethodMux routes incoming JSON-RPC requests to different [Handler] implementations
// based on the method name specified in the [Request]. Method names are case-sensitive.
//
// A MethodMux itself implements the [Handler] interface, allowing it to be used
// directly as the main handler for an [RPCServer] or nested within other muxes.
// If no specific handler is found for a method, and a default handler has been set
// via [MethodMux.SetDefault] or [MethodMux.SetDefaultFunc], the default handler is invoked.
// Otherwise, [ErrMethodNotFound] is returned.
//
// MethodMux is safe for concurrent use. Reads ([MethodMux.Handle]) and writes
// ([MethodMux.Register], [MethodMux.Replace], [MethodMux.Delete], etc.) can happen
// concurrently. Use [NewMethodMux] to create instances.
type MethodMux struct {
	// defaultHandler stores the Handler to use when no specific method match is found.
	defaultHandler atomic.Value // Stores Handler interface type
	// mux stores the mapping from method names (string) to Handlers.
	mux sync.Map // map[string]Handler
}

// NewMethodMux creates and returns a new, initialized [*MethodMux].
func NewMethodMux() *MethodMux {
	return &MethodMux{} // Initializes with an empty sync.Map
}

// Register associates a [Handler] with a specific method name.
// It returns [ErrMethodAlreadyExists] if a handler for the given method name
// is already registered. Method names are case-sensitive.
// Use [MethodMux.Replace] to overwrite an existing handler.
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

// Replace registers or replaces the handler for the given method name.
// If a handler for the method already exists, it is overwritten.
// Method names are case-sensitive.
//
// Example:
//
//	mux := jsonrpc2.NewMethodMux()
//	mux.Register("myMethod", oldHandler)
//	mux.Replace("myMethod", newHandler) // Overwrites oldHandler
//	mux.Replace("newMethod", handler)   // Registers handler for newMethod
func (mm *MethodMux) Replace(method string, handler Handler) {
	mm.mux.Store(method, handler)
}

// RegisterFunc associates a handler function `f` with a specific method name.
// It wraps the function using [NewFuncHandler] before registering it.
// It returns [ErrMethodAlreadyExists] if a handler for the given method name
// is already registered. Method names are case-sensitive.
// Use [MethodMux.ReplaceFunc] to overwrite an existing handler.
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

// ReplaceFunc registers or replaces the handler for the given method name
// using the provided handler function `f`. It wraps the function using [NewFuncHandler].
// If a handler for the method already exists, it is overwritten.
// Method names are case-sensitive.
//
// Example:
//
//	mux := jsonrpc2.NewMethodMux()
//	echoFunc := func(ctx context.Context, req *jsonrpc2.Request) (any, error) { return req.Params, nil }
//	mux.ReplaceFunc("utils.echo", echoFunc) // Registers or replaces handler for utils.echo
func (mm *MethodMux) ReplaceFunc(method string, f func(context.Context, *Request) (any, error)) {
	mm.Replace(method, NewFuncHandler(f))
}

// Methods returns a slice containing the names of all currently registered methods.
// The order of methods in the slice is not guaranteed.
func (mm *MethodMux) Methods() []string {
	methods := make([]string, 0) // Pre-allocate slightly? sync.Map doesn't provide count easily.

	//nolint:errcheck //Internally managed, key is never not a string
	mm.mux.Range(func(key, _ any) bool { methods = append(methods, key.(string)); return true })

	return methods
}

// Delete removes the handler associated with the given method name.
// If no handler is registered for the method, this is a no-op.
//
// Example:
//
//	mux.Delete("obsoleteMethod")
func (mm *MethodMux) Delete(method string) {
	mm.mux.Delete(method)
}

// SetDefault sets the default [Handler] to be called when no specific handler
// is found for a requested method.
// Calling with `nil` removes the default handler.
//
// Example:
//
//	// Handler that returns a custom error for unknown methods
//	unknownMethodHandler := jsonrpc2.NewFuncHandler(func(ctx context.Context, req *jsonrpc2.Request) (any, error) {
//	    return nil, jsonrpc2.NewError(1234, "Unknown method requested: "+req.Method)
//	})
//	mux.SetDefault(unknownMethodHandler)
//
//	// To remove the default handler:
//	// mux.SetDefault(nil)
func (mm *MethodMux) SetDefault(handler Handler) {
	if handler == nil {
		// Use Delete to clear the value, ensuring Load returns nil, false later.
		mm.defaultHandler.Delete()
	} else {
		mm.defaultHandler.Store(handler)
	}
}

// SetDefaultFunc sets the default handler function to be called when no specific
// handler is found. It wraps the function `f` using [NewFuncHandler].
// Calling with `nil` removes the default handler.
//
// Example:
//
//	mux.SetDefaultFunc(func(ctx context.Context, req *jsonrpc2.Request) (any, error) {
//	    log.Printf("Warning: Unhandled method called: %s", req.Method)
//	    return nil, jsonrpc2.ErrMethodNotFound // Still return standard error
//	})
//
//	// To remove the default handler:
//	// mux.SetDefaultFunc(nil)
func (mm *MethodMux) SetDefaultFunc(f func(context.Context, *Request) (any, error)) {
	if f == nil {
		mm.SetDefault(nil)
	} else {
		mm.SetDefault(NewFuncHandler(f))
	}
}

// Handle implements the [Handler] interface for MethodMux.
// It looks up the handler registered for the method specified in the [Request].
// If a specific handler is found, Handle delegates the request processing to it.
// If no specific handler is found, it checks if a default handler is set.
// If a default handler exists, it delegates to the default handler.
// If neither a specific nor a default handler is found, it returns [ErrMethodNotFound].
func (mm *MethodMux) Handle(ctx context.Context, req *Request) (any, error) {
	// Load the handler associated with the specific method name.
	value, ok := mm.mux.Load(req.Method)
	if ok {
		// Specific handler found, delegate to it.
		// Type assertion is safe due to how handlers are stored.
		return value.(Handler).Handle(ctx, req)
	}

	// No specific handler found, try loading the default handler.
	defaultValue := mm.defaultHandler.Load()
	if defaultValue != nil {
		// Default handler exists, delegate to it.
		// Type assertion is safe due to how the default handler is stored.
		return defaultValue.(Handler).Handle(ctx, req)
	}

	// No specific handler and no default handler found.
	return nil, ErrMethodNotFound
}
