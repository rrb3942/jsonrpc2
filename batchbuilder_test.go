package jsonrpc2

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Test Setup ---

// setupTestBuilder creates a BatchBuilder with a mock client and pool.
// It returns the builder, the mock client, and the mock pool.
func setupTestBuilder[B BatchBuildable](t *testing.T, size int) (*BatchBuilder[B], *Client, *mockPool) {
	t.Helper()

	pool := &mockPool{}
	client := setupTestClient(pool) // setupTestClient is from client_test.go

	var builder *BatchBuilder[B]

	// Determine the type B to create the correct builder
	var zero B
	switch any(zero).(type) {
	case *Request:
		//nolint:errcheck //This is for testing
		builder = any(client.NewRequestBatch(size)).(*BatchBuilder[B])
	case *Notification:
		//nolint:errcheck //This is for testing
		builder = any(client.NewNotificationBatch(size)).(*BatchBuilder[B])
	default:
		t.Fatalf("Unsupported BatchBuildable type in test setup")
	}

	require.NotNil(t, builder, "Builder should not be nil")
	require.NotNil(t, builder.parent, "Builder parent should be set")
	require.NotNil(t, builder.Batch, "Builder batch should be initialized")

	return builder, client, pool
}

// --- Tests ---

func TestBatchBuilder_Add_Request(t *testing.T) {
	builder, client, _ := setupTestBuilder[*Request](t, 5)
	client.id.Store(99) // Set initial ID for predictability

	method1 := "test.method1"
	params1 := map[string]int{"a": 1}
	expectedParams1 := NewParamsRaw(json.RawMessage(`{"a":1}`))

	method2 := "test.method2"
	params2 := []string{"b", "c"}
	expectedParams2 := NewParamsRaw(json.RawMessage(`["b","c"]`))

	// Add first request
	err := builder.Add(method1, params1)
	require.NoError(t, err)
	assert.Len(t, builder.Batch, 1, "Batch should have 1 item")

	req1 := builder.Batch[0]
	assert.NotNil(t, req1)
	assert.Equal(t, method1, req1.Method)
	assert.Equal(t, int64(100), req1.ID.Value(), "First ID should be 100") // nextID() increments
	assert.Equal(t, expectedParams1.value, req1.Params.value)

	// Add second request
	err = builder.Add(method2, params2)
	require.NoError(t, err)
	assert.Len(t, builder.Batch, 2, "Batch should have 2 items")

	req2 := builder.Batch[1]
	assert.NotNil(t, req2)
	assert.Equal(t, method2, req2.Method)
	assert.Equal(t, int64(101), req2.ID.Value(), "Second ID should be 101")
	assert.Equal(t, expectedParams2.value, req2.Params.value)

	// Add request with nil params
	err = builder.Add("method3", nil)
	require.NoError(t, err)
	assert.Len(t, builder.Batch, 3)
	req3 := builder.Batch[2]
	assert.True(t, req3.Params.IsZero(), "Params should be zero for nil input")
	assert.Equal(t, int64(102), req3.ID.Value(), "Third ID should be 102")
}

func TestBatchBuilder_Add_Notification(t *testing.T) {
	builder, _, _ := setupTestBuilder[*Notification](t, 5)

	method1 := "notify.method1"
	params1 := struct{ Status string }{"active"}
	expectedParams1 := NewParamsRaw(json.RawMessage(`{"Status":"active"}`))

	method2 := "notify.method2"
	params2 := []int{10, 20}
	expectedParams2 := NewParamsRaw(json.RawMessage(`[10,20]`))

	// Add first notification
	err := builder.Add(method1, params1)
	require.NoError(t, err)
	assert.Len(t, builder.Batch, 1, "Batch should have 1 item")

	notif1 := builder.Batch[0]
	assert.NotNil(t, notif1)
	assert.Equal(t, method1, notif1.Method)
	assert.True(t, notif1.ID.IsZero(), "Notification ID should be zero")
	assert.Equal(t, expectedParams1.value, notif1.Params.value)

	// Add second notification
	err = builder.Add(method2, params2)
	require.NoError(t, err)
	assert.Len(t, builder.Batch, 2, "Batch should have 2 items")

	notif2 := builder.Batch[1]
	assert.NotNil(t, notif2)
	assert.Equal(t, method2, notif2.Method)
	assert.True(t, notif2.ID.IsZero(), "Notification ID should be zero")
	assert.Equal(t, expectedParams2.value, notif2.Params.value)

	// Add notification with nil params
	err = builder.Add("method3", nil)
	require.NoError(t, err)
	assert.Len(t, builder.Batch, 3)
	notif3 := builder.Batch[2]
	assert.True(t, notif3.Params.IsZero(), "Params should be zero for nil input")
	assert.True(t, notif3.ID.IsZero(), "Notification ID should be zero")
}

func TestBatchBuilder_Add_Error(t *testing.T) {
	t.Run("InvalidParamsType", func(t *testing.T) {
		builder, _, _ := setupTestBuilder[*Request](t, 1)
		err := builder.Add("method", "not an object or array") // Invalid param type
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidParamsType)
		assert.Len(t, builder.Batch, 0, "Batch should be empty after error")
	})

	t.Run("MarshalError", func(t *testing.T) {
		builder, _, _ := setupTestBuilder[*Notification](t, 1)
		err := builder.Add("method", func() {}) // Cannot marshal function
		require.Error(t, err)
		assert.IsType(t, &json.UnsupportedTypeError{}, errors.Unwrap(err)) // Check underlying marshal error
		assert.Len(t, builder.Batch, 0, "Batch should be empty after error")
	})
}

func TestBatchBuilder_Call_Request(t *testing.T) {
	ctx := t.Context()
	builder, client, pool := setupTestBuilder[*Request](t, 2)
	client.id.Store(0) // Reset ID counter

	// Add items to the batch
	err := builder.Add("method1", nil)
	require.NoError(t, err)
	err = builder.Add("method2", []int{1})
	require.NoError(t, err)
	require.Len(t, builder.Batch, 2)

	expectedReqBatch := builder.Batch // Keep a reference before Call

	// Mock the pool response
	expectedRespBatch := NewBatch[*Response](2)
	expectedRespBatch.Add(NewResponseWithResult(int64(1), "res1"))          // ID matches first request
	expectedRespBatch.Add(NewResponseWithError(int64(2), ErrInternalError)) // ID matches second request

	poolErr := errors.New("pool call failed")

	t.Run("Success_NoTimeout", func(t *testing.T) {
		var callBatchCalled bool

		pool.callBatchFunc = func(c context.Context, batch Batch[*Request]) (Batch[*Response], error) {
			callBatchCalled = true

			assert.Equal(t, ctx, c)
			assert.Equal(t, expectedReqBatch, batch) // Check if the correct batch was passed

			return expectedRespBatch, nil
		}
		pool.callBatchTimeoutFunc = nil // Ensure timeout func is not called

		respBatch, err := builder.Call(ctx)
		require.NoError(t, err)
		assert.True(t, callBatchCalled, "pool.CallBatch should have been called")
		assert.Equal(t, expectedRespBatch, respBatch)
	})

	t.Run("Success_WithTimeout", func(t *testing.T) {
		timeout := 100 * time.Millisecond
		client.SetDefaultTimeout(timeout)

		defer client.SetDefaultTimeout(0) // Reset timeout

		var callBatchTimeoutCalled bool

		pool.callBatchTimeoutFunc = func(c context.Context, tout time.Duration, batch Batch[*Request]) (Batch[*Response], error) {
			callBatchTimeoutCalled = true

			assert.Equal(t, ctx, c)
			assert.Equal(t, timeout, tout)
			assert.Equal(t, expectedReqBatch, batch)

			return expectedRespBatch, nil
		}
		pool.callBatchFunc = nil // Ensure base func is not called

		respBatch, err := builder.Call(ctx)
		require.NoError(t, err)
		assert.True(t, callBatchTimeoutCalled, "pool.CallBatchWithTimeout should have been called")
		assert.Equal(t, expectedRespBatch, respBatch)
	})

	t.Run("Error_NoTimeout", func(t *testing.T) {
		client.SetDefaultTimeout(0) // Ensure no timeout

		var callBatchCalled bool

		pool.callBatchFunc = func(_ context.Context, _ Batch[*Request]) (Batch[*Response], error) {
			callBatchCalled = true
			return nil, poolErr
		}
		pool.callBatchTimeoutFunc = nil

		respBatch, err := builder.Call(ctx)
		require.Error(t, err)
		assert.True(t, callBatchCalled, "pool.CallBatch should have been called")
		assert.Equal(t, poolErr, err)
		assert.Nil(t, respBatch)
	})

	t.Run("Error_WithTimeout", func(t *testing.T) {
		timeout := 50 * time.Millisecond
		client.SetDefaultTimeout(timeout)

		defer client.SetDefaultTimeout(0)

		var callBatchTimeoutCalled bool

		pool.callBatchTimeoutFunc = func(_ context.Context, _ time.Duration, _ Batch[*Request]) (Batch[*Response], error) {
			callBatchTimeoutCalled = true
			return nil, poolErr
		}
		pool.callBatchFunc = nil

		respBatch, err := builder.Call(ctx)
		require.Error(t, err)
		assert.True(t, callBatchTimeoutCalled, "pool.CallBatchWithTimeout should have been called")
		assert.Equal(t, poolErr, err)
		assert.Nil(t, respBatch)
	})
}

func TestBatchBuilder_Call_Notification(t *testing.T) {
	ctx := t.Context()
	builder, client, pool := setupTestBuilder[*Notification](t, 2)

	// Add items to the batch
	err := builder.Add("notify1", nil)
	require.NoError(t, err)
	err = builder.Add("notify2", map[string]bool{"ok": true})
	require.NoError(t, err)
	require.Len(t, builder.Batch, 2)

	expectedNotifyBatch := builder.Batch // Keep a reference before Call
	poolErr := errors.New("pool notify failed")

	t.Run("Success_NoTimeout", func(t *testing.T) {
		var notifyBatchCalled bool

		pool.notifyBatchFunc = func(c context.Context, batch Batch[*Notification]) error {
			notifyBatchCalled = true

			assert.Equal(t, ctx, c)
			assert.Equal(t, expectedNotifyBatch, batch) // Check if the correct batch was passed

			return nil
		}
		pool.notifyBatchTimeoutFunc = nil // Ensure timeout func is not called

		respBatch, err := builder.Call(ctx) // Response batch is always nil for notifications
		require.NoError(t, err)
		assert.True(t, notifyBatchCalled, "pool.NotifyBatch should have been called")
		assert.Nil(t, respBatch, "Response batch should be nil for notifications")
	})

	t.Run("Success_WithTimeout", func(t *testing.T) {
		timeout := 100 * time.Millisecond
		client.SetDefaultTimeout(timeout)

		defer client.SetDefaultTimeout(0) // Reset timeout

		var notifyBatchTimeoutCalled bool

		pool.notifyBatchTimeoutFunc = func(c context.Context, tout time.Duration, batch Batch[*Notification]) error {
			notifyBatchTimeoutCalled = true

			assert.Equal(t, ctx, c)
			assert.Equal(t, timeout, tout)
			assert.Equal(t, expectedNotifyBatch, batch)

			return nil
		}
		pool.notifyBatchFunc = nil // Ensure base func is not called

		respBatch, err := builder.Call(ctx)
		require.NoError(t, err)
		assert.True(t, notifyBatchTimeoutCalled, "pool.NotifyBatchWithTimeout should have been called")
		assert.Nil(t, respBatch, "Response batch should be nil for notifications")
	})

	t.Run("Error_NoTimeout", func(t *testing.T) {
		client.SetDefaultTimeout(0) // Ensure no timeout

		var notifyBatchCalled bool

		pool.notifyBatchFunc = func(_ context.Context, _ Batch[*Notification]) error {
			notifyBatchCalled = true
			return poolErr
		}
		pool.notifyBatchTimeoutFunc = nil

		respBatch, err := builder.Call(ctx)
		require.Error(t, err)
		assert.True(t, notifyBatchCalled, "pool.NotifyBatch should have been called")
		assert.Equal(t, poolErr, err)
		assert.Nil(t, respBatch)
	})

	t.Run("Error_WithTimeout", func(t *testing.T) {
		timeout := 50 * time.Millisecond
		client.SetDefaultTimeout(timeout)

		defer client.SetDefaultTimeout(0)

		var notifyBatchTimeoutCalled bool

		pool.notifyBatchTimeoutFunc = func(_ context.Context, _ time.Duration, _ Batch[*Notification]) error {
			notifyBatchTimeoutCalled = true
			return poolErr
		}
		pool.notifyBatchFunc = nil

		respBatch, err := builder.Call(ctx)
		require.Error(t, err)
		assert.True(t, notifyBatchTimeoutCalled, "pool.NotifyBatchWithTimeout should have been called")
		assert.Equal(t, poolErr, err)
		assert.Nil(t, respBatch)
	})
}

func TestBatchBuilder_Reuse(t *testing.T) {
	ctx := t.Context()
	builder, client, pool := setupTestBuilder[*Request](t, 5)
	client.id.Store(0) // Start ID at 0

	// --- First Batch ---
	err := builder.Add("methodA", nil)
	require.NoError(t, err)
	require.Len(t, builder.Batch, 1)
	assert.Equal(t, int64(1), builder.Batch[0].ID.Value())

	// Mock pool for first call
	var callCount1 int

	pool.callBatchFunc = func(_ context.Context, batch Batch[*Request]) (Batch[*Response], error) {
		callCount1++

		assert.Len(t, batch, 1)
		assert.Equal(t, int64(1), batch[0].ID.Value())

		resp := NewBatch[*Response](1)
		resp.Add(batch[0].ResponseWithResult("A"))

		return resp, nil
	}

	// Call first time
	respBatch1, err := builder.Call(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, callCount1, "Pool should be called once for first batch")
	require.Len(t, respBatch1, 1)
	assert.Equal(t, int64(1), respBatch1[0].ID.Value())

	// Check builder state *before* reset
	assert.Len(t, builder.Batch, 1, "Batch length should be 1 before reset")
	assert.Equal(t, int64(1), builder.Batch[0].ID.Value(), "Batch item should still exist before reset")

	// --- Reset ---
	builder.Reset()
	assert.Len(t, builder.Batch, 0, "Batch length should be 0 after reset")
	assert.GreaterOrEqual(t, cap(builder.Batch), 1, "Capacity should remain after reset") // Capacity check

	// --- Second Batch ---
	err = builder.Add("methodB", []int{1})
	require.NoError(t, err)
	err = builder.Add("methodC", nil)
	require.NoError(t, err)
	require.Len(t, builder.Batch, 2)
	assert.Equal(t, int64(2), builder.Batch[0].ID.Value(), "ID should continue from 2")
	assert.Equal(t, int64(3), builder.Batch[1].ID.Value(), "ID should continue to 3")

	// Mock pool for second call
	var callCount2 int

	pool.callBatchFunc = func(_ context.Context, batch Batch[*Request]) (Batch[*Response], error) {
		callCount2++

		assert.Len(t, batch, 2)
		assert.Equal(t, int64(2), batch[0].ID.Value())
		assert.Equal(t, int64(3), batch[1].ID.Value())

		resp := NewBatch[*Response](2)
		resp.Add(batch[0].ResponseWithResult("B"))
		resp.Add(batch[1].ResponseWithResult("C"))

		return resp, nil
	}

	// Call second time
	respBatch2, err := builder.Call(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, callCount2, "Pool should be called once for second batch")
	require.Len(t, respBatch2, 2)
	assert.Equal(t, int64(2), respBatch2[0].ID.Value())
	assert.Equal(t, int64(3), respBatch2[1].ID.Value())

	// Check builder state after second call
	assert.Len(t, builder.Batch, 2, "Batch length should be 2 after second call")
}
