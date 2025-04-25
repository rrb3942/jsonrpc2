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
	assert.Nil(t, res, "Handle should return nil result for an unknown method")
	assert.True(t, errors.Is(err, ErrMethodNotFound), "Error should be ErrMethodNotFound")
}
