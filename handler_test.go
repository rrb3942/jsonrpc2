package jsonrpc2

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockHandler is a simple handler for testing.
type mockHandler struct {
	handleFunc func(context.Context, *Request) (any, error)
	serverStop context.CancelFunc
	panicFlag  atomic.Bool
}

func (h *mockHandler) Handle(ctx context.Context, req *Request) (any, error) {
	if h.serverStop != nil {
		defer h.serverStop()
	}

	if h.panicFlag.Load() {
		panic("handler panic!")
	}

	if h.handleFunc != nil {
		return h.handleFunc(ctx, req)
	}
	// Default echo handler
	return fmt.Sprintf("handled %s", req.Method), nil
}

func (h *mockHandler) TriggerPanic() {
	h.panicFlag.Store(true)
}

func (h *mockHandler) ResetPanic() {
	h.panicFlag.Store(false)
}

func TestMethodMux_Register(t *testing.T) {
	mux := NewMethodMux()
	handler1 := &mockHandler{}
	methodName := "testMethod"

	// Test successful registration
	err := mux.Register(methodName, handler1)
	require.NoError(t, err, "Register should succeed for a new method")

	// Test duplicate registration
	handler2 := &mockHandler{}
	err = mux.Register(methodName, handler2)
	require.Error(t, err, "Register should fail for an existing method")
	assert.True(t, errors.Is(err, ErrMethodAlreadyExists), "Error should be ErrMethodAlreadyExists")
	assert.Contains(t, err.Error(), methodName, "Error message should contain method name")
}

func TestMethodMux_Replace(t *testing.T) {
	mux := NewMethodMux()
	methodName := "testMethod"
	handler1 := &mockHandler{handleFunc: func(_ context.Context, _ *Request) (any, error) { return "handler1", nil }}
	handler2 := &mockHandler{handleFunc: func(_ context.Context, _ *Request) (any, error) { return "handler2", nil }}

	// Register initial handler
	err := mux.Register(methodName, handler1)
	require.NoError(t, err)

	// Replace the handler
	mux.Replace(methodName, handler2)

	// Verify the new handler is used
	req := &Request{Method: methodName}
	res, err := mux.Handle(t.Context(), req)
	require.NoError(t, err)
	assert.Equal(t, "handler2", res, "Handle should use the replaced handler")

	// Replace a non-existent handler (should just register it)
	mux.Replace("newMethod", handler1)
	reqNew := &Request{Method: "newMethod"}
	res, err = mux.Handle(t.Context(), reqNew)
	require.NoError(t, err)
	assert.Equal(t, "handler1", res, "Replace should register a handler if it doesn't exist")
}

func TestMethodMux_RegisterFunc(t *testing.T) {
	mux := NewMethodMux()
	methodName := "testFuncMethod"
	handlerFunc := func(_ context.Context, _ *Request) (any, error) {
		return "func result", nil
	}

	// Test successful registration
	err := mux.RegisterFunc(methodName, handlerFunc)
	require.NoError(t, err, "RegisterFunc should succeed for a new method")

	// Test duplicate registration
	err = mux.RegisterFunc(methodName, handlerFunc) // Registering func again
	require.Error(t, err, "RegisterFunc should fail for an existing method")
	assert.True(t, errors.Is(err, ErrMethodAlreadyExists), "Error should be ErrMethodAlreadyExists")

	// Test duplicate registration with Register
	handler1 := &mockHandler{}
	err = mux.Register(methodName, handler1)
	require.Error(t, err, "Register should fail for an existing method previously registered with RegisterFunc")
	assert.True(t, errors.Is(err, ErrMethodAlreadyExists), "Error should be ErrMethodAlreadyExists")
	assert.Contains(t, err.Error(), methodName, "Error message should contain method name")
}

func TestMethodMux_ReplaceFunc(t *testing.T) {
	mux := NewMethodMux()
	methodName := "testMethod"
	func1 := func(_ context.Context, _ *Request) (any, error) { return "func1", nil }
	func2 := func(_ context.Context, _ *Request) (any, error) { return "func2", nil }

	// Register initial func
	err := mux.RegisterFunc(methodName, func1)
	require.NoError(t, err)

	// Replace the func
	mux.ReplaceFunc(methodName, func2)

	// Verify the new func is used
	req := &Request{Method: methodName}
	res, err := mux.Handle(t.Context(), req)
	require.NoError(t, err)
	assert.Equal(t, "func2", res, "Handle should use the replaced func")

	// Replace a non-existent func (should just register it)
	mux.ReplaceFunc("newMethod", func1)
	reqNew := &Request{Method: "newMethod"}
	res, err = mux.Handle(t.Context(), reqNew)
	require.NoError(t, err)
	assert.Equal(t, "func1", res, "ReplaceFunc should register a func if it doesn't exist")
}

func TestMethodMux_Delete(t *testing.T) {
	mux := NewMethodMux()
	methodName := "testMethod"
	handler := &mockHandler{}

	// Register a handler
	err := mux.Register(methodName, handler)
	require.NoError(t, err)

	// Verify it's handled
	req := &Request{Method: methodName}
	_, err = mux.Handle(t.Context(), req)
	require.NoError(t, err)

	// Delete the handler
	mux.Delete(methodName)

	// Verify it's no longer handled
	_, err = mux.Handle(t.Context(), req)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrMethodNotFound), "Handle should return ErrMethodNotFound after Delete")

	// Delete a non-existent handler (should be no-op)
	mux.Delete("nonExistentMethod")
}

func TestMethodMux_Methods(t *testing.T) {
	mux := NewMethodMux()
	methods := []string{"method1", "method2", "anotherMethod"}

	for _, m := range methods {
		err := mux.Register(m, &mockHandler{})
		require.NoError(t, err)
	}

	registeredMethods := mux.Methods()
	assert.ElementsMatch(t, methods, registeredMethods, "Methods should return all registered method names")

	// Test after delete
	mux.Delete(methods[1])
	expectedAfterDelete := []string{methods[0], methods[2]}
	registeredMethods = mux.Methods()
	assert.ElementsMatch(t, expectedAfterDelete, registeredMethods, "Methods should not include deleted methods")

	// Test empty mux
	emptyMux := NewMethodMux()
	assert.Empty(t, emptyMux.Methods(), "Methods should return empty slice for an empty mux")
}

func TestFuncHandler_Handle(t *testing.T) {
	expectedResult := "test result"

	var expectedErr = errors.New("test error")

	var called bool

	handlerFunc := func(_ context.Context, req *Request) (any, error) {
		called = true

		if req.Method == "methodWithError" {
			return nil, expectedErr
		}

		return expectedResult, nil
	}

	mux := NewMethodMux()
	err := mux.RegisterFunc("testMethod", handlerFunc)
	require.NoError(t, err)
	err = mux.RegisterFunc("methodWithError", handlerFunc)
	require.NoError(t, err)

	// Test successful call
	called = false
	reqSuccess := &Request{Method: "testMethod"}
	result, err := mux.Handle(t.Context(), reqSuccess)
	assert.NoError(t, err, "Handle should not return an error for successful func call")
	assert.Equal(t, expectedResult, result, "Handle should return the result from the func")
	assert.True(t, called, "Handler function should have been called")

	// Test call resulting in error
	called = false
	reqError := &Request{Method: "methodWithError"}
	result, err = mux.Handle(t.Context(), reqError)
	assert.Error(t, err, "Handle should return an error when the func returns an error")
	assert.Nil(t, result, "Handle should return nil result when the func returns an error")
	assert.Equal(t, expectedErr, err, "Handle should return the error from the func")
	assert.True(t, called, "Handler function should have been called")
}

func TestMethodMux_Handle(t *testing.T) {
	mux := NewMethodMux()
	ctx := t.Context()

	method1 := "method1"
	result1 := "result1"
	handler1 := &mockHandler{
		handleFunc: func(_ context.Context, req *Request) (any, error) {
			assert.Equal(t, method1, req.Method)
			return result1, nil
		},
	}
	err := mux.Register(method1, handler1)
	require.NoError(t, err)

	method2 := "method2"
	result2 := "result2"
	handlerFunc2 := func(_ context.Context, req *Request) (any, error) {
		assert.Equal(t, method2, req.Method)
		return result2, nil
	}
	err = mux.RegisterFunc(method2, handlerFunc2)
	require.NoError(t, err)

	// Test handling registered method (Handler interface)
	req1 := &Request{Method: method1}
	res, err := mux.Handle(ctx, req1)
	require.NoError(t, err)
	assert.Equal(t, result1, res)

	// Test handling registered method (func)
	req2 := &Request{Method: method2}
	res, err = mux.Handle(ctx, req2)
	require.NoError(t, err)
	assert.Equal(t, result2, res)

	// Test handling unregistered method
	reqUnknown := &Request{Method: "unknownMethod"}
	res, err = mux.Handle(ctx, reqUnknown)
	require.Error(t, err, "Handle should return an error for an unknown method")
	assert.Nil(t, res, "Handle should return nil result for an unknown method without default handler")
	assert.True(t, errors.Is(err, ErrMethodNotFound), "Error should be ErrMethodNotFound without default handler")

	// Test default handler (Handler interface)
	defaultResult := "default result"
	defaultHandler := &mockHandler{
		handleFunc: func(_ context.Context, req *Request) (any, error) {
			assert.Equal(t, "unknownMethod", req.Method) // Should receive the original method name
			return defaultResult, nil
		},
	}
	mux.SetDefault(defaultHandler)
	res, err = mux.Handle(ctx, reqUnknown)
	require.NoError(t, err, "Handle should not return an error when default handler succeeds")
	assert.Equal(t, defaultResult, res, "Handle should return result from default handler")

	// Test default handler (func)
	defaultFuncResult := "default func result"
	defaultFuncCalled := false
	mux.SetDefaultFunc(func(_ context.Context, req *Request) (any, error) {
		defaultFuncCalled = true
		assert.Equal(t, "anotherUnknown", req.Method)
		return defaultFuncResult, nil
	})
	reqAnotherUnknown := &Request{Method: "anotherUnknown"}
	res, err = mux.Handle(ctx, reqAnotherUnknown)
	require.NoError(t, err, "Handle should not return an error when default func handler succeeds")
	assert.Equal(t, defaultFuncResult, res, "Handle should return result from default func handler")
	assert.True(t, defaultFuncCalled, "Default handler func should have been called")

	// Test that specific handler takes precedence over default handler
	res, err = mux.Handle(ctx, req1) // req1 uses method1 which has a specific handler
	require.NoError(t, err)
	assert.Equal(t, result1, res, "Specific handler should take precedence over default handler")

	// Test removing default handler
	mux.SetDefault(nil)
	res, err = mux.Handle(ctx, reqUnknown)
	require.Error(t, err, "Handle should return an error for an unknown method after removing default handler")
	assert.Nil(t, res, "Handle should return nil result after removing default handler")
	assert.True(t, errors.Is(err, ErrMethodNotFound), "Error should be ErrMethodNotFound after removing default handler")

	// Test removing default handler func
	mux.SetDefaultFunc(func(_ context.Context, _ *Request) (any, error) { return "temp", nil }) // Set it first
	mux.SetDefaultFunc(nil)                                                                     // Remove it
	res, err = mux.Handle(ctx, reqUnknown)
	require.Error(t, err, "Handle should return an error for an unknown method after removing default func handler")
	assert.Nil(t, res, "Handle should return nil result after removing default func handler")
	assert.True(t, errors.Is(err, ErrMethodNotFound), "Error should be ErrMethodNotFound after removing default func handler")
}
