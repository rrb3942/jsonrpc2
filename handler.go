package jsonrpc2

import (
	"context"
	"errors"
)

var ErrMethodAlreadyExists = errors.New("method already exits in mux")

// A Handler accepts and process a [*Request], potentially return a result on success, or an error on failure.
// It is called asynchronously from a [*Connection] to handle client requests.
//
// If the result is a [*Response] or [RawResponse] it will be used directly as a response, otherwise an appropriate response will be built with the provided result.
//
// If error is, or wraps an [Error] the [Error] will be used directly in the response.
// Other values for error will automatically be converted to an [ErrInternalError] with the data field populated with the error string.
//
// Notifications automaically drop any responses or errors.
type Handler interface {
	Handle(context.Context, *Request) (any, error)
}

// NewFuncHandler returns a [Handler] that serves using the given function.
//
//nolint:ireturn //Helper function
func NewFuncHandler(handler func(context.Context, *Request) (any, error)) Handler {
	return &funcHandler{funcHandle: handler}
}

// funcHandler is used to wrap a function into a [Handler].
type funcHandler struct {
	funcHandle func(context.Context, *Request) (any, error)
}

// Handle implements [Handler].
func (fh *funcHandler) Handle(ctx context.Context, req *Request) (any, error) {
	return fh.funcHandle(ctx, req)
}

// MethodMux is an implementation of [Handler] that will route a given method to a given [Handler].
//
// Method routing is case sensitive.
//
// It should not be modified once the [Server] has started.
type MethodMux struct {
	mux map[string]Handler
}

// NewMethodMux returns a new [*MethodMux] that is ready to use.
func NewMethodMux() *MethodMux {
	return &MethodMux{mux: make(map[string]Handler)}
}

// Register adds the handler to the mux for the given method.
// If a handler already exists for the given method it returns [ErrMethodAlreadyExists].
func (mm *MethodMux) Register(method string, handler Handler) error {
	if _, ok := mm.mux[method]; ok {
		return ErrMethodAlreadyExists
	}

	mm.mux[method] = handler

	return nil
}

// RegisterFunc adds the given function to handle the given method.
// If a handler already exists for the given method it returns [ErrMethodAlreadyExists].
func (mm *MethodMux) RegisterFunc(method string, handler func(context.Context, *Request) (any, error)) error {
	if _, ok := mm.mux[method]; ok {
		return ErrMethodAlreadyExists
	}

	mm.mux[method] = NewFuncHandler(handler)

	return nil
}

// Handle implements [Handler] and servers the given request, routing it to the
// registered handler for the method.
func (mm *MethodMux) Handle(ctx context.Context, req *Request) (any, error) {
	if handler, ok := mm.mux[req.Method]; ok {
		return handler.Handle(ctx, req)
	}

	return nil, ErrMethodNotFound
}
