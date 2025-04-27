package jsonrpc2

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mocks ---

// mockPool implements the subset of ClientPool methods used by Client.
type mockPool struct {
	callFunc               func(ctx context.Context, req *Request) (*Response, error)
	callWithTimeoutFunc    func(ctx context.Context, timeout time.Duration, req *Request) (*Response, error)
	notifyFunc             func(ctx context.Context, notif *Notification) error
	notifyWithTimeoutFunc  func(ctx context.Context, timeout time.Duration, notif *Notification) error
	callBatchFunc          func(ctx context.Context, batch Batch[*Request]) (Batch[*Response], error)
	callBatchTimeoutFunc   func(ctx context.Context, timeout time.Duration, batch Batch[*Request]) (Batch[*Response], error)
	notifyBatchFunc        func(ctx context.Context, batch Batch[*Notification]) error
	notifyBatchTimeoutFunc func(ctx context.Context, timeout time.Duration, batch Batch[*Notification]) error
	closeFunc              func()
	closeCalled            atomic.Bool
}

func (m *mockPool) Call(ctx context.Context, req *Request) (*Response, error) {
	if m.callFunc != nil {
		return m.callFunc(ctx, req)
	}

	return nil, errors.New("mockPool.Call not implemented")
}

func (m *mockPool) CallWithTimeout(ctx context.Context, timeout time.Duration, req *Request) (*Response, error) {
	if m.callWithTimeoutFunc != nil {
		return m.callWithTimeoutFunc(ctx, timeout, req)
	}

	return nil, errors.New("mockPool.CallWithTimeout not implemented")
}

func (m *mockPool) Notify(ctx context.Context, notif *Notification) error {
	if m.notifyFunc != nil {
		return m.notifyFunc(ctx, notif)
	}

	return errors.New("mockPool.Notify not implemented")
}

func (m *mockPool) NotifyWithTimeout(ctx context.Context, timeout time.Duration, notif *Notification) error {
	if m.notifyWithTimeoutFunc != nil {
		return m.notifyWithTimeoutFunc(ctx, timeout, notif)
	}

	return errors.New("mockPool.NotifyWithTimeout not implemented")
}

func (m *mockPool) CallBatch(ctx context.Context, batch Batch[*Request]) (Batch[*Response], error) {
	if m.callBatchFunc != nil {
		return m.callBatchFunc(ctx, batch)
	}

	return nil, errors.New("mockPool.CallBatch not implemented")
}

func (m *mockPool) CallBatchWithTimeout(ctx context.Context, timeout time.Duration, batch Batch[*Request]) (Batch[*Response], error) {
	if m.callBatchTimeoutFunc != nil {
		return m.callBatchTimeoutFunc(ctx, timeout, batch)
	}

	return nil, errors.New("mockPool.CallBatchWithTimeout not implemented")
}

func (m *mockPool) NotifyBatch(ctx context.Context, batch Batch[*Notification]) error {
	if m.notifyBatchFunc != nil {
		return m.notifyBatchFunc(ctx, batch)
	}

	return errors.New("mockPool.NotifyBatch not implemented")
}

func (m *mockPool) NotifyBatchWithTimeout(ctx context.Context, timeout time.Duration, batch Batch[*Notification]) error {
	if m.notifyBatchTimeoutFunc != nil {
		return m.notifyBatchTimeoutFunc(ctx, timeout, batch)
	}

	return errors.New("mockPool.NotifyBatchWithTimeout not implemented")
}

func (m *mockPool) Close() {
	m.closeCalled.Store(true)

	if m.closeFunc != nil {
		m.closeFunc()
	}
}

// --- Test Helper ---

// setupTestClient creates a Client with the provided mock pool implementation.
func setupTestClient(pool *mockPool) *Client {
	// Since Client.pool is now an interface (poolClientInterface),
	// and *mockPool implements this interface, we can assign it directly.
	return &Client{pool: pool}
}

// --- Tests ---

func TestMakeParamsFromAny(t *testing.T) {
	tests := []struct {
		input     any
		expectErr error
		expected  Params
		name      string
	}{
		{
			name:     "nil",
			input:    nil,
			expected: Params{}, // Zero value signifies omitted params
		},
		{
			name:     "struct (object)",
			input:    struct{ Name string }{"test"},
			expected: NewParamsRaw(json.RawMessage(`{"Name":"test"}`)),
		},
		{
			name:     "map (object)",
			input:    map[string]int{"a": 1},
			expected: NewParamsRaw(json.RawMessage(`{"a":1}`)),
		},
		{
			name:     "slice (array)",
			input:    []int{1, 2, 3},
			expected: NewParamsRaw(json.RawMessage(`[1,2,3]`)),
		},
		{
			name:     "array (array)",
			input:    [2]string{"a", "b"},
			expected: NewParamsRaw(json.RawMessage(`["a","b"]`)),
		},
		{
			name:      "string (invalid)",
			input:     "hello",
			expectErr: ErrInvalidParamsType,
		},
		{
			name:      "int (invalid)",
			input:     123,
			expectErr: ErrInvalidParamsType,
		},
		{
			name:      "bool (invalid)",
			input:     true,
			expectErr: ErrInvalidParamsType,
		},
		{
			name:      "float (invalid)",
			input:     1.23,
			expectErr: ErrInvalidParamsType,
		},
		{
			name:      "function (marshal error)",
			input:     func() {},
			expectErr: &json.UnsupportedTypeError{}, // Expecting marshal error
		},
		{
			name:      "channel (marshal error)",
			input:     make(chan int),
			expectErr: &json.UnsupportedTypeError{}, // Expecting marshal error
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			params, err := makeParamsFromAny(tc.input)

			if tc.expectErr != nil {
				require.Error(t, err)
				// Use errors.Is for wrapped errors, reflect.TypeOf for specific error types
				if errors.Is(tc.expectErr, ErrInvalidParamsType) {
					assert.ErrorIs(t, err, ErrInvalidParamsType)
				} else {
					assert.IsType(t, tc.expectErr, errors.Unwrap(err)) // Check underlying marshal error type
				}
			} else {
				require.NoError(t, err)
				// Compare raw messages for equality
				expectedRaw, _ := json.Marshal(tc.expected.value)
				actualRaw, _ := json.Marshal(params.value)
				assert.JSONEq(t, string(expectedRaw), string(actualRaw))
			}
		})
	}
}

func TestClient_nextID(t *testing.T) {
	client := &Client{} // No pool needed for this test
	client.id.Store(10) // Start at 10

	assert.Equal(t, int64(11), client.nextID(), "First ID should be 11")
	assert.Equal(t, int64(12), client.nextID(), "Second ID should be 12")
	assert.Equal(t, int64(13), client.nextID(), "Third ID should be 13")

	// Test potential wrap-around (though unlikely in practice)
	client.id.Store(uint32(0xFFFFFFFF))
	assert.Equal(t, int64(0), client.nextID(), "ID should wrap around") // Wraps to 0
	assert.Equal(t, int64(1), client.nextID(), "ID should be 1 after wrap")
}

func TestClient_SetDefaultTimeout(t *testing.T) {
	client := &Client{}
	assert.Equal(t, time.Duration(0), client.defaultTimeout, "Initial timeout should be 0")

	client.SetDefaultTimeout(5 * time.Second)
	assert.Equal(t, 5*time.Second, client.defaultTimeout, "Timeout should be set to 5s")

	client.SetDefaultTimeout(0)
	assert.Equal(t, time.Duration(0), client.defaultTimeout, "Timeout should be reset to 0")

	client.SetDefaultTimeout(-1 * time.Second)
	assert.Equal(t, -1*time.Second, client.defaultTimeout, "Timeout should allow negative (disabled)")
}

func TestClient_Close(t *testing.T) {
	pool := &mockPool{}
	client := setupTestClient(pool)

	client.Close()
	assert.True(t, pool.closeCalled.Load(), "pool.Close() should have been called")

	// Test double close is safe (mock doesn't track multiple calls, just that it was called)
	client.Close()
	assert.True(t, pool.closeCalled.Load(), "pool.Close() should still be true after double close")
}

func TestClient_Call(t *testing.T) {
	ctx := t.Context()
	method := "test.method"
	params := map[string]string{"p": "v"}
	expectedReqParams := NewParamsRaw(json.RawMessage(`{"p":"v"}`))

	t.Run("Success_NoTimeout", func(t *testing.T) {
		pool := &mockPool{}
		client := setupTestClient(pool)
		client.id.Store(0) // Start ID at 0

		pool.callFunc = func(c context.Context, req *Request) (*Response, error) {
			assert.Equal(t, ctx, c)
			assert.Equal(t, method, req.Method)
			assert.Equal(t, int64(1), req.ID.Value()) // First ID
			assert.Equal(t, expectedReqParams.value, req.Params.value)
			// Simulate a real response where Result contains RawMessage
			// Marshal the Go string result into JSON bytes
			resultBytes, err := json.Marshal("result")
			require.NoError(t, err, "Failed to marshal mock result")
			// Create the response using the request's ID and the raw message result
			resp := req.ResponseWithResult(json.RawMessage(resultBytes))

			return resp, nil
		}

		resp, err := client.Call(ctx, method, params)
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, int64(1), resp.ID.Value()) // Compare against int64
		// Compare the underlying Go value
		var result string
		err = resp.Result.Unmarshal(&result)
		require.NoError(t, err, "Unmarshalling result should succeed")
		assert.Equal(t, "result", result)
	})

	t.Run("Success_WithDefaultTimeout", func(t *testing.T) {
		pool := &mockPool{}
		client := setupTestClient(pool)
		client.id.Store(10) // Start ID at 10

		timeout := 100 * time.Millisecond
		client.SetDefaultTimeout(timeout)

		pool.callWithTimeoutFunc = func(c context.Context, tout time.Duration, req *Request) (*Response, error) {
			assert.Equal(t, ctx, c) // Original context is passed
			assert.Equal(t, timeout, tout)
			assert.Equal(t, method, req.Method)
			assert.Equal(t, int64(11), req.ID.Value()) // Next ID
			assert.Equal(t, expectedReqParams.value, req.Params.value)
			// Simulate a real response where Result contains RawMessage
			// Marshal the Go string result into JSON bytes
			resultBytes, err := json.Marshal("result_timeout")
			require.NoError(t, err, "Failed to marshal mock result")
			// Create the response using the request's ID and the raw message result
			resp := req.ResponseWithResult(json.RawMessage(resultBytes))

			return resp, nil
		}

		resp, err := client.Call(ctx, method, params)
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, int64(11), resp.ID.Value()) // Compare against int64
		// Compare the underlying Go value
		var result string
		err = resp.Result.Unmarshal(&result)
		require.NoError(t, err, "Unmarshalling result should succeed")
		assert.Equal(t, "result_timeout", result)
	})

	t.Run("Error_InvalidParams", func(t *testing.T) {
		pool := &mockPool{} // Pool methods shouldn't be called
		client := setupTestClient(pool)

		_, err := client.Call(ctx, method, "invalid params") // String is not object/array
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidParamsType)
	})

	t.Run("Error_PoolCallFailed", func(t *testing.T) {
		pool := &mockPool{}
		client := setupTestClient(pool)
		poolErr := errors.New("pool call failed")

		pool.callFunc = func(_ context.Context, _ *Request) (*Response, error) {
			return nil, poolErr
		}

		_, err := client.Call(ctx, method, params)
		require.Error(t, err)
		assert.Equal(t, poolErr, err) // Should return the exact error from the pool
	})

	t.Run("Error_PoolCallWithTimeoutFailed", func(t *testing.T) {
		pool := &mockPool{}
		client := setupTestClient(pool)
		timeout := 50 * time.Millisecond
		client.SetDefaultTimeout(timeout)

		poolErr := errors.New("pool call timeout failed")

		pool.callWithTimeoutFunc = func(_ context.Context, _ time.Duration, _ *Request) (*Response, error) {
			return nil, poolErr
		}

		_, err := client.Call(ctx, method, params)
		require.Error(t, err)
		assert.Equal(t, poolErr, err)
	})
}

func TestClient_Notify(t *testing.T) {
	ctx := t.Context()
	method := "notify.method"
	params := []int{1, 2}
	expectedNotifyParams := NewParamsRaw(json.RawMessage(`[1,2]`))

	t.Run("Success_NoTimeout", func(t *testing.T) {
		pool := &mockPool{}
		client := setupTestClient(pool)

		var notifyCalled bool

		pool.notifyFunc = func(c context.Context, notif *Notification) error {
			notifyCalled = true

			assert.Equal(t, ctx, c)
			assert.Equal(t, method, notif.Method)
			assert.True(t, notif.ID.IsZero(), "Notification ID should be zero")
			assert.Equal(t, expectedNotifyParams.value, notif.Params.value)

			return nil
		}

		err := client.Notify(ctx, method, params)
		require.NoError(t, err)
		assert.True(t, notifyCalled, "pool.Notify should have been called")
	})

	t.Run("Success_WithDefaultTimeout", func(t *testing.T) {
		pool := &mockPool{}
		client := setupTestClient(pool)
		timeout := 100 * time.Millisecond
		client.SetDefaultTimeout(timeout)

		var notifyTimeoutCalled bool

		pool.notifyWithTimeoutFunc = func(c context.Context, tout time.Duration, notif *Notification) error {
			notifyTimeoutCalled = true

			assert.Equal(t, ctx, c) // Original context
			assert.Equal(t, timeout, tout)
			assert.Equal(t, method, notif.Method)
			assert.True(t, notif.ID.IsZero())
			assert.Equal(t, expectedNotifyParams.value, notif.Params.value)

			return nil
		}

		err := client.Notify(ctx, method, params)
		require.NoError(t, err)
		assert.True(t, notifyTimeoutCalled, "pool.NotifyWithTimeout should have been called")
	})

	t.Run("Error_InvalidParams", func(t *testing.T) {
		pool := &mockPool{} // Pool methods shouldn't be called
		client := setupTestClient(pool)

		err := client.Notify(ctx, method, 123.45) // Float is not object/array
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidParamsType)
	})

	t.Run("Error_PoolNotifyFailed", func(t *testing.T) {
		pool := &mockPool{}
		client := setupTestClient(pool)
		poolErr := errors.New("pool notify failed")

		pool.notifyFunc = func(_ context.Context, _ *Notification) error {
			return poolErr
		}

		err := client.Notify(ctx, method, params)
		require.Error(t, err)
		assert.Equal(t, poolErr, err)
	})

	t.Run("Error_PoolNotifyWithTimeoutFailed", func(t *testing.T) {
		pool := &mockPool{}
		client := setupTestClient(pool)
		timeout := 50 * time.Millisecond
		client.SetDefaultTimeout(timeout)

		poolErr := errors.New("pool notify timeout failed")

		pool.notifyWithTimeoutFunc = func(_ context.Context, _ time.Duration, _ *Notification) error {
			return poolErr
		}

		err := client.Notify(ctx, method, params)
		require.Error(t, err)
		assert.Equal(t, poolErr, err)
	})
}

func TestClient_NewBatchRequest(t *testing.T) {
	client := &Client{} // Pool not needed for builder creation check
	size := 5
	builder := client.NewRequestBatch(size)

	require.NotNil(t, builder)
	assert.Same(t, client, builder.parent, "Builder parent should be the client")
	assert.NotNil(t, builder.Batch, "Builder should have an initialized batch")
	assert.IsType(t, Batch[*Request]{}, builder.Batch, "Batch should be of type *Request")
	assert.Equal(t, 0, len(builder.Batch), "Batch should be empty initially")
	assert.Equal(t, size, cap(builder.Batch), "Batch capacity should match size hint")
}

func TestClient_NewNotificationBatch(t *testing.T) {
	client := &Client{} // Pool not needed for builder creation check
	size := 3
	builder := client.NewNotificationBatch(size)

	require.NotNil(t, builder)
	assert.Same(t, client, builder.parent, "Builder parent should be the client")
	assert.NotNil(t, builder.Batch, "Builder should have an initialized batch")
	assert.IsType(t, Batch[*Notification]{}, builder.Batch, "Batch should be of type *Notification")
	assert.Equal(t, 0, len(builder.Batch), "Batch should be empty initially")
	assert.Equal(t, size, cap(builder.Batch), "Batch capacity should match size hint")
}

// Test internal batch helpers via BatchBuilder.Call.
func TestClient_BatchHelpers_Timeout(t *testing.T) {
	ctx := t.Context()
	timeout := 50 * time.Millisecond

	t.Run("callBatch_WithTimeout", func(t *testing.T) {
		pool := &mockPool{}
		client := setupTestClient(pool)
		client.SetDefaultTimeout(timeout)

		var timeoutFuncCalled bool

		pool.callBatchTimeoutFunc = func(c context.Context, tout time.Duration, batch Batch[*Request]) (Batch[*Response], error) {
			timeoutFuncCalled = true

			assert.Equal(t, ctx, c)
			assert.Equal(t, timeout, tout)
			assert.Equal(t, 1, len(batch)) // Check batch content passed

			return NewBatch[*Response](0), nil
		}

		builder := client.NewRequestBatch(1)
		err := builder.Add("method", nil) // Check error from Add
		require.NoError(t, err, "builder.Add should succeed")
		require.Equal(t, 1, len(builder.Batch), "Batch length should be 1 before Call")

		_, err = builder.Call(ctx) // This uses client.callBatch internally
		require.NoError(t, err, "builder.Call should succeed")
		assert.True(t, timeoutFuncCalled, "pool.CallBatchWithTimeout should have been called")
	})

	t.Run("callBatch_NoTimeout", func(t *testing.T) {
		pool := &mockPool{}
		client := setupTestClient(pool)
		// No default timeout set
		var baseFuncCalled bool

		pool.callBatchFunc = func(c context.Context, batch Batch[*Request]) (Batch[*Response], error) {
			baseFuncCalled = true

			assert.Equal(t, ctx, c)
			assert.Equal(t, 1, len(batch))

			return NewBatch[*Response](0), nil
		}

		builder := client.NewRequestBatch(1)
		err := builder.Add("method", nil) // Check error from Add
		require.NoError(t, err, "builder.Add should succeed")
		require.Equal(t, 1, len(builder.Batch), "Batch length should be 1 before Call")

		_, err = builder.Call(ctx)
		require.NoError(t, err, "builder.Call should succeed")
		assert.True(t, baseFuncCalled, "pool.CallBatch should have been called")
	})

	t.Run("notifyBatch_WithTimeout", func(t *testing.T) {
		pool := &mockPool{}
		client := setupTestClient(pool)
		client.SetDefaultTimeout(timeout)

		var timeoutFuncCalled bool

		pool.notifyBatchTimeoutFunc = func(c context.Context, tout time.Duration, batch Batch[*Notification]) error {
			timeoutFuncCalled = true

			assert.Equal(t, ctx, c)
			assert.Equal(t, timeout, tout)
			assert.Equal(t, 1, len(batch))

			return nil
		}

		builder := client.NewNotificationBatch(1)
		err := builder.Add("method", nil) // Check error from Add
		require.NoError(t, err, "builder.Add should succeed")
		require.Equal(t, 1, len(builder.Batch), "Batch length should be 1 before Call")

		_, err = builder.Call(ctx) // This uses client.notifyBatch internally
		require.NoError(t, err, "builder.Call should succeed")
		assert.True(t, timeoutFuncCalled, "pool.NotifyBatchWithTimeout should have been called")
	})

	t.Run("notifyBatch_NoTimeout", func(t *testing.T) {
		pool := &mockPool{}
		client := setupTestClient(pool)
		// No default timeout set
		var baseFuncCalled bool

		pool.notifyBatchFunc = func(c context.Context, batch Batch[*Notification]) error {
			baseFuncCalled = true

			assert.Equal(t, ctx, c)
			assert.Equal(t, 1, len(batch))

			return nil
		}

		builder := client.NewNotificationBatch(1)
		err := builder.Add("method", nil) // Check error from Add
		require.NoError(t, err, "builder.Add should succeed")
		require.Equal(t, 1, len(builder.Batch), "Batch length should be 1 before Call")

		_, err = builder.Call(ctx)
		require.NoError(t, err, "builder.Call should succeed")
		assert.True(t, baseFuncCalled, "pool.NotifyBatch should have been called")
	})
}

// --- Concurrency Test ---

// TestClient_nextID_Concurrent tests that IDs are unique under concurrency.
// Note: This doesn't guarantee atomicity perfectly but increases confidence.
func TestClient_nextID_Concurrent(t *testing.T) {
	client := &Client{}
	numGoroutines := 100
	idsPerGoroutine := 1000
	totalIDs := numGoroutines * idsPerGoroutine
	idMap := make(map[int64]bool, totalIDs)

	var mu sync.Mutex // Protect map access

	var wg sync.WaitGroup

	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()

			localIDs := make([]int64, idsPerGoroutine)
			for j := 0; j < idsPerGoroutine; j++ {
				localIDs[j] = client.nextID()
			}
			// Add generated IDs to the shared map
			mu.Lock()
			for _, id := range localIDs {
				if _, exists := idMap[id]; exists {
					t.Errorf("Duplicate ID generated: %d", id) // Use t.Errorf for concurrent tests
				}

				idMap[id] = true
			}
			mu.Unlock()
		}()
	}

	wg.Wait()

	// Check if the total number of unique IDs matches the expected count
	mu.Lock()
	finalCount := len(idMap)
	mu.Unlock()
	assert.Equal(t, totalIDs, finalCount, "Expected %d unique IDs, but got %d", totalIDs, finalCount)
}
