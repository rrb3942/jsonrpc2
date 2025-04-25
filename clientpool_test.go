package jsonrpc2

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/puddle/v2" // Added import
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mocks ---

// mockPoolClient implements the minimum required methods for pool testing.
type mockPoolClient struct {
	callFunc      func(ctx context.Context, rpc any, isNotify bool) (json.RawMessage, error)
	unmarshalFunc func(data []byte, v any) error
	closeFunc     func() error
	closeCount    atomic.Int32
}

func (m *mockPoolClient) Encode(ctx context.Context, v any) error {
	// Simulate encoding by calling the internal call function logic directly for testing
	_, err := m.callFunc(ctx, v, false) // Assume not a notification for simplicity here
	// We only care about the error propagation for encode simulation
	return err
}

func (m *mockPoolClient) Decode(ctx context.Context, v any) error {
	// Simulate decoding by calling the internal call function logic
	raw, err := m.callFunc(ctx, nil, false) // Pass nil request for decode simulation
	if err != nil {
		return err
	}
	// Simulate unmarshalling the raw response
	return m.unmarshalFunc(raw, v)
}

func (m *mockPoolClient) Unmarshal(data []byte, v any) error {
	if m.unmarshalFunc != nil {
		return m.unmarshalFunc(data, v)
	}
	// Default unmarshal if none provided
	return json.Unmarshal(data, v)
}

func (m *mockPoolClient) Close() error {
	m.closeCount.Add(1)
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

// call simulates the internal client call logic for testing pool retries.
func (m *mockPoolClient) call(ctx context.Context, rpc any, isNotify bool) (json.RawMessage, error) {
	if m.callFunc != nil {
		return m.callFunc(ctx, rpc, isNotify)
	}
	// Default success if no func provided
	if isNotify {
		return nil, nil
	}
	// Return a dummy response for calls
	return json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":"ok"}`), nil
}

// --- Test Helper ---

func setupTestPool(t *testing.T, config ClientPoolConfig, dialFunc func(ctx context.Context, uri string) (*Client, error)) (*ClientPool, func()) {
	t.Helper()
	if dialFunc == nil {
		// Default mock dialer if none provided
		dialFunc = func(ctx context.Context, uri string) (*Client, error) {
			mockC := &mockPoolClient{}
			// Wrap mock in actual Client struct using its Encoder/Decoder interfaces
			return NewClient(mockC, mockC), nil
		}
	}

	pool, err := NewClientPoolWithDialer(context.Background(), config, dialFunc)
	require.NoError(t, err, "Failed to create client pool")

	cleanup := func() {
		pool.Close()
	}
	return pool, cleanup
}

// --- Tests ---

func TestNewClientPool(t *testing.T) {
	t.Run("Defaults", func(t *testing.T) {
		dialCount := atomic.Int32{}
		dialFunc := func(ctx context.Context, uri string) (*Client, error) {
			dialCount.Add(1)
			mockC := &mockPoolClient{}
			return NewClient(mockC, mockC), nil
		}
		config := ClientPoolConfig{URI: "mock://"}
		pool, cleanup := setupTestPool(t, config, dialFunc)
		defer cleanup()

		// Cannot directly access puddle config's MaxIdleTime after creation.
		// assert.Equal(t, time.Duration(DefaultPoolIdleTimeout)*time.Second, pool.pool.Config().MaxIdleTime, "Default IdleTimeout mismatch")
		assert.Equal(t, 2, pool.retries, "Default Retries mismatch (should be 1 + 1)") // Default is 1, plus the initial try
		assert.Zero(t, pool.pool.Stat().TotalResources(), "Pool should be empty initially")
		assert.Zero(t, dialCount.Load(), "Dial should not happen without AcquireOnCreate")
	})

	t.Run("AcquireOnCreate_Success", func(t *testing.T) {
		dialCount := atomic.Int32{}
		dialFunc := func(ctx context.Context, uri string) (*Client, error) {
			dialCount.Add(1)
			mockC := &mockPoolClient{}
			return NewClient(mockC, mockC), nil
		}
		config := ClientPoolConfig{URI: "mock://", AcquireOnCreate: true}
		pool, err := NewClientPoolWithDialer(context.Background(), config, dialFunc)
		require.NoError(t, err)
		defer pool.Close()

		assert.EqualValues(t, 1, dialCount.Load(), "Dial should happen with AcquireOnCreate")
		assert.EqualValues(t, 1, pool.pool.Stat().TotalResources(), "Pool should have one resource")
		assert.EqualValues(t, 1, pool.pool.Stat().IdleResources(), "Pool resource should be idle")
	})

	t.Run("AcquireOnCreate_Failure", func(t *testing.T) {
		dialErr := errors.New("dial failed")
		dialFunc := func(ctx context.Context, uri string) (*Client, error) {
			return nil, dialErr
		}
		config := ClientPoolConfig{URI: "mock://", AcquireOnCreate: true}
		_, err := NewClientPoolWithDialer(context.Background(), config, dialFunc)
		require.Error(t, err)
		assert.ErrorIs(t, err, dialErr, "Expected dial error during AcquireOnCreate")
	})

	t.Run("CustomTimeouts", func(t *testing.T) {
		config := ClientPoolConfig{
			URI:         "mock://",
			IdleTimeout: 5 * time.Second,
			DialTimeout: 1 * time.Second,
		}
		pool, cleanup := setupTestPool(t, config, nil) // Use default dialer
		defer cleanup()

		// Note: We can't easily check DialTimeout without a slow dialer.
		// We also cannot directly access puddle config's MaxIdleTime after creation.
		// assert.Equal(t, 5*time.Second, pool.pool.Config().MaxIdleTime)
		// We can check that the pool's idle timer is configured based on the input.
		pool.mu.Lock()
		hasIdleTimer := pool.idle != nil
		pool.mu.Unlock()
		assert.True(t, hasIdleTimer, "Pool should have an idle timer with positive IdleTimeout")
	})

	t.Run("NegativeIdleTimeout", func(t *testing.T) {
		config := ClientPoolConfig{
			URI:         "mock://",
			IdleTimeout: -1 * time.Second, // Disable idle timeout
		}
		pool, cleanup := setupTestPool(t, config, nil)
		defer cleanup()
		assert.Nil(t, pool.idle, "Idle timer should be nil when timeout is negative")
		// Cannot directly access puddle config's MaxIdleTime after creation.
		// assert.Equal(t, time.Duration(0), pool.pool.Config().MaxIdleTime, "Puddle MaxIdleTime should be 0")
	})
}

func TestClientPool_Call_Success(t *testing.T) {
	callCount := atomic.Int32{}
	mockC := &mockPoolClient{
		callFunc: func(ctx context.Context, rpc any, isNotify bool) (json.RawMessage, error) {
			callCount.Add(1)
			req, ok := rpc.(*Request)
			require.True(t, ok, "Expected *Request type")
			assert.Equal(t, "testMethod", req.Method)
			assert.False(t, isNotify)
			// Simulate successful response
			reqID, _ := req.ID.Int64() // Extract ID value
			resp := NewResponseWithResult(reqID, "success")
			raw, _ := json.Marshal(resp)
			return raw, nil
		},
		unmarshalFunc: func(data []byte, v any) error {
			// Need to unmarshal the raw response into the Response struct
			return json.Unmarshal(data, v)
		},
	}
	dialFunc := func(ctx context.Context, uri string) (*Client, error) {
		return NewClient(mockC, mockC), nil
	}
	config := ClientPoolConfig{URI: "mock://", Retries: 1} // 1 retry = 2 attempts
	pool, cleanup := setupTestPool(t, config, dialFunc)
	defer cleanup()

	req := NewRequest(int64(1), "testMethod")
	resp, err := pool.Call(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.EqualValues(t, 1, callCount.Load())
	id, _ := resp.ID.Int64()
	assert.Equal(t, int64(1), id)
	var result string
	err = resp.Result.Unmarshal(&result)
	require.NoError(t, err)
	assert.Equal(t, "success", result)
	assert.EqualValues(t, 1, pool.pool.Stat().AcquireCount(), "Acquire count mismatch")
	assert.EqualValues(t, 0, pool.pool.Stat().AcquiredResources(), "Resources should be released")
}

func TestClientPool_Call_RetryableError(t *testing.T) {
	callCount := atomic.Int32{}
	dialCount := atomic.Int32{}
	closeCount := atomic.Int32{}

	retryableErr := io.EOF // Use a known retryable error

	dialFunc := func(ctx context.Context, uri string) (*Client, error) {
		dialCount.Add(1)
		currentDial := dialCount.Load()
		mockC := &mockPoolClient{
			callFunc: func(ctx context.Context, rpc any, isNotify bool) (json.RawMessage, error) {
				callCount.Add(1)
				if currentDial == 1 { // Fail on the first client
					return nil, retryableErr
				}
				// Succeed on the second client (after retry)
				req := rpc.(*Request)
				reqID, _ := req.ID.Int64() // Extract ID value
				resp := NewResponseWithResult(reqID, "success_after_retry")
				raw, _ := json.Marshal(resp)
				return raw, nil
			},
			unmarshalFunc: func(data []byte, v any) error {
				return json.Unmarshal(data, v)
			},
			closeFunc: func() error {
				closeCount.Add(1)
				return nil
			},
		}
		return NewClient(mockC, mockC), nil
	}

	config := ClientPoolConfig{URI: "mock://", Retries: 1} // 1 retry = 2 attempts
	pool, cleanup := setupTestPool(t, config, dialFunc)
	defer cleanup()

	req := NewRequest(int64(1), "retryMethod")
	resp, err := pool.Call(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.EqualValues(t, 2, callCount.Load(), "Call count should be 2 (initial + retry)")
	assert.EqualValues(t, 2, dialCount.Load(), "Dial count should be 2 (initial + retry)")
	assert.EqualValues(t, 1, closeCount.Load(), "Close count should be 1 (first client destroyed)")

	var result string
	err = resp.Result.Unmarshal(&result)
	require.NoError(t, err)
	assert.Equal(t, "success_after_retry", result)

	assert.EqualValues(t, 2, pool.pool.Stat().AcquireCount(), "Acquire count mismatch")
	assert.EqualValues(t, 1, pool.pool.Stat().DestroyedResources(), "Destroy count mismatch")
	assert.EqualValues(t, 0, pool.pool.Stat().AcquiredResources(), "Resources should be released")
}

func TestClientPool_Call_NonRetryableError(t *testing.T) {
	callCount := atomic.Int32{}
	dialCount := atomic.Int32{}
	closeCount := atomic.Int32{}

	nonRetryableErr := context.Canceled // Use a known non-retryable error

	dialFunc := func(ctx context.Context, uri string) (*Client, error) {
		dialCount.Add(1)
		mockC := &mockPoolClient{
			callFunc: func(ctx context.Context, rpc any, isNotify bool) (json.RawMessage, error) {
				callCount.Add(1)
				return nil, nonRetryableErr // Fail immediately
			},
			closeFunc: func() error {
				closeCount.Add(1)
				return nil
			},
		}
		return NewClient(mockC, mockC), nil
	}

	config := ClientPoolConfig{URI: "mock://", Retries: 3} // More retries shouldn't matter
	pool, cleanup := setupTestPool(t, config, dialFunc)
	defer cleanup()

	req := NewRequest(int64(1), "nonRetryMethod")
	_, err := pool.Call(context.Background(), req)

	require.Error(t, err)
	assert.ErrorIs(t, err, nonRetryableErr)
	assert.False(t, errors.Is(err, ErrRetriesExceeded), "Should not be ErrRetriesExceeded")

	assert.EqualValues(t, 1, callCount.Load(), "Call count should be 1")
	assert.EqualValues(t, 1, dialCount.Load(), "Dial count should be 1")
	// Client is destroyed even on non-retryable errors if an error occurred during the call
	assert.EqualValues(t, 1, closeCount.Load(), "Close count should be 1")
	assert.EqualValues(t, 1, pool.pool.Stat().DestroyedResources(), "Destroy count mismatch")
}

func TestClientPool_Call_RetriesExceeded(t *testing.T) {
	callCount := atomic.Int32{}
	dialCount := atomic.Int32{}
	closeCount := atomic.Int32{}

	persistentErr := net.ErrClosed // A retryable error

	dialFunc := func(ctx context.Context, uri string) (*Client, error) {
		dialCount.Add(1)
		mockC := &mockPoolClient{
			callFunc: func(ctx context.Context, rpc any, isNotify bool) (json.RawMessage, error) {
				callCount.Add(1)
				return nil, persistentErr // Always fail
			},
			closeFunc: func() error {
				closeCount.Add(1)
				return nil
			},
		}
		return NewClient(mockC, mockC), nil
	}

	config := ClientPoolConfig{URI: "mock://", Retries: 2} // 2 retries = 3 attempts
	pool, cleanup := setupTestPool(t, config, dialFunc)
	defer cleanup()

	req := NewRequest(int64(1), "failMethod")
	_, err := pool.Call(context.Background(), req)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRetriesExceeded, "Expected ErrRetriesExceeded")
	assert.ErrorIs(t, err, persistentErr, "Expected wrapped original error") // Check original error is wrapped

	assert.EqualValues(t, 3, callCount.Load(), "Call count should be 3 (initial + 2 retries)")
	assert.EqualValues(t, 3, dialCount.Load(), "Dial count should be 3")
	assert.EqualValues(t, 3, closeCount.Load(), "Close count should be 3 (all clients destroyed)")
	assert.EqualValues(t, 3, pool.pool.Stat().DestroyedResources(), "Destroy count mismatch")
}

func TestClientPool_Notify_Success(t *testing.T) {
	callCount := atomic.Int32{}
	mockC := &mockPoolClient{
		callFunc: func(ctx context.Context, rpc any, isNotify bool) (json.RawMessage, error) {
			callCount.Add(1)
			_, ok := rpc.(*Notification)
			require.True(t, ok, "Expected *Notification type")
			assert.True(t, isNotify)
			return nil, nil // Notify succeeds
		},
	}
	dialFunc := func(ctx context.Context, uri string) (*Client, error) {
		return NewClient(mockC, mockC), nil
	}
	config := ClientPoolConfig{URI: "mock://"}
	pool, cleanup := setupTestPool(t, config, dialFunc)
	defer cleanup()

	notify := NewNotification("testNotify")
	err := pool.Notify(context.Background(), notify)

	require.NoError(t, err)
	assert.EqualValues(t, 1, callCount.Load())
}

func TestClientPool_Notify_RetryableError(t *testing.T) {
	callCount := atomic.Int32{}
	dialCount := atomic.Int32{}
	closeCount := atomic.Int32{}
	retryableErr := os.ErrClosed // Another retryable error

	dialFunc := func(ctx context.Context, uri string) (*Client, error) {
		dialCount.Add(1)
		currentDial := dialCount.Load()
		mockC := &mockPoolClient{
			callFunc: func(ctx context.Context, rpc any, isNotify bool) (json.RawMessage, error) {
				callCount.Add(1)
				if currentDial == 1 {
					return nil, retryableErr // Fail first time
				}
				return nil, nil // Succeed second time
			},
			closeFunc: func() error {
				closeCount.Add(1)
				return nil
			},
		}
		return NewClient(mockC, mockC), nil
	}

	config := ClientPoolConfig{URI: "mock://", Retries: 1}
	pool, cleanup := setupTestPool(t, config, dialFunc)
	defer cleanup()

	notify := NewNotification("retryNotify")
	err := pool.Notify(context.Background(), notify)

	require.NoError(t, err)
	assert.EqualValues(t, 2, callCount.Load())
	assert.EqualValues(t, 2, dialCount.Load())
	assert.EqualValues(t, 1, closeCount.Load())
}

func TestClientPool_Notify_RetriesExceeded(t *testing.T) {
	callCount := atomic.Int32{}
	persistentErr := io.ErrUnexpectedEOF // Yet another retryable error

	dialFunc := func(ctx context.Context, uri string) (*Client, error) {
		mockC := &mockPoolClient{
			callFunc: func(ctx context.Context, rpc any, isNotify bool) (json.RawMessage, error) {
				callCount.Add(1)
				return nil, persistentErr // Always fail
			},
		}
		return NewClient(mockC, mockC), nil
	}

	config := ClientPoolConfig{URI: "mock://", Retries: 1} // 1 retry = 2 attempts
	pool, cleanup := setupTestPool(t, config, dialFunc)
	defer cleanup()

	notify := NewNotification("failNotify")
	err := pool.Notify(context.Background(), notify)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRetriesExceeded)
	assert.ErrorIs(t, err, persistentErr)
	assert.EqualValues(t, 2, callCount.Load()) // Initial + 1 retry
}

func TestClientPool_ContextCancel_Acquire(t *testing.T) {
	// Use a dialer that blocks until context is cancelled
	dialStarted := make(chan struct{})
	dialFunc := func(ctx context.Context, uri string) (*Client, error) {
		close(dialStarted) // Signal that dial has started
		<-ctx.Done()       // Wait for cancellation
		return nil, ctx.Err()
	}

	config := ClientPoolConfig{URI: "mock://", MaxSize: 1}
	pool, cleanup := setupTestPool(t, config, dialFunc)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(1)
	var err error
	go func() {
		defer wg.Done()
		// This will block in the dialer
		_, err = pool.Call(ctx, NewRequest(int64(1), "test"))
	}()

	<-dialStarted // Wait for the dialer to start blocking
	cancel()      // Cancel the context
	wg.Wait()     // Wait for the Call goroutine to finish

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestClientPool_ContextCancel_DuringCall(t *testing.T) {
	callStarted := make(chan struct{})
	callCtxDone := make(chan struct{})

	mockC := &mockPoolClient{
		callFunc: func(ctx context.Context, rpc any, isNotify bool) (json.RawMessage, error) {
			close(callStarted)
			select {
			case <-ctx.Done():
				close(callCtxDone)
				return nil, ctx.Err()
			case <-time.After(5 * time.Second): // Timeout for safety
				return nil, errors.New("test timeout")
			}
		},
	}
	dialFunc := func(ctx context.Context, uri string) (*Client, error) {
		return NewClient(mockC, mockC), nil
	}
	config := ClientPoolConfig{URI: "mock://", MaxSize: 1}
	pool, cleanup := setupTestPool(t, config, dialFunc)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(1)
	var err error
	go func() {
		defer wg.Done()
		_, err = pool.Call(ctx, NewRequest(int64(1), "test"))
	}()

	<-callStarted // Wait for the callFunc to start blocking
	cancel()      // Cancel the context
	<-callCtxDone // Wait for the callFunc to acknowledge cancellation
	wg.Wait()     // Wait for the Call goroutine to finish

	require.Error(t, err)
	// Because the error happens *during* the call, the client might be destroyed.
	// The error returned might be the context error directly, or wrapped if destroy fails.
	assert.ErrorIs(t, err, context.Canceled)
	// Non-retryable, so shouldn't be retries exceeded
	assert.False(t, errors.Is(err, ErrRetriesExceeded))
}

func TestClientPool_IdleTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping idle timeout test in short mode")
	}

	closeCount := atomic.Int32{}
	dialCount := atomic.Int32{}
	dialFunc := func(ctx context.Context, uri string) (*Client, error) {
		dialCount.Add(1)
		mockC := &mockPoolClient{
			closeFunc: func() error {
				closeCount.Add(1)
				return nil
			},
		}
		return NewClient(mockC, mockC), nil
	}

	idleTime := 50 * time.Millisecond
	config := ClientPoolConfig{
		URI:         "mock://",
		IdleTimeout: idleTime,
		MaxSize:     1,
	}
	pool, cleanup := setupTestPool(t, config, dialFunc)
	defer cleanup()

	// Acquire and release a client to make it idle
	res, err := pool.pool.Acquire(context.Background())
	require.NoError(t, err)
	assert.EqualValues(t, 1, dialCount.Load())
	res.Release()
	assert.EqualValues(t, 1, pool.pool.Stat().IdleResources())

	// Wait longer than the idle timeout
	time.Sleep(idleTime * 3)

	// Check if the client was closed (destroyed)
	assert.Eventually(t, func() bool {
		return closeCount.Load() == 1
	}, 2*idleTime, 5*time.Millisecond, "Client was not closed after idle timeout")

	assert.EqualValues(t, 0, pool.pool.Stat().IdleResources(), "Idle resources should be 0 after cleanup")
	assert.EqualValues(t, 0, pool.pool.Stat().TotalResources(), "Total resources should be 0 after cleanup")

	// Acquire again, should dial a new one
	res2, err := pool.pool.Acquire(context.Background())
	require.NoError(t, err)
	assert.EqualValues(t, 2, dialCount.Load(), "Should dial a new client")
	res2.Release()
}

func TestClientPool_MaxSize(t *testing.T) {
	maxSize := int32(2)
	dialCount := atomic.Int32{}
	blockDial := make(chan struct{}) // Channel to block dials

	dialFunc := func(ctx context.Context, uri string) (*Client, error) {
		dialCount.Add(1)
		// Block subsequent dials if requested
		if dialCount.Load() > maxSize {
			select {
			case <-blockDial: // Wait until unblocked
			case <-ctx.Done(): // Respect context cancellation
				return nil, ctx.Err()
			}
		}
		mockC := &mockPoolClient{}
		return NewClient(mockC, mockC), nil
	}

	config := ClientPoolConfig{URI: "mock://", MaxSize: maxSize}
	pool, cleanup := setupTestPool(t, config, dialFunc)
	defer cleanup()

	var resources []*puddle.Resource[*Client]
	// Acquire up to MaxSize
	for i := int32(0); i < maxSize; i++ {
		res, err := pool.pool.Acquire(context.Background())
		require.NoError(t, err)
		resources = append(resources, res)
	}
	assert.EqualValues(t, maxSize, dialCount.Load())
	assert.EqualValues(t, maxSize, pool.pool.Stat().AcquiredResources())
	assert.EqualValues(t, maxSize, pool.pool.Stat().TotalResources())

	// Try to acquire one more, should block or fail if context times out
	acquireCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := pool.pool.Acquire(acquireCtx)
	require.Error(t, err, "Acquire should fail when pool is full")
	assert.ErrorIs(t, err, context.DeadlineExceeded, "Error should be context deadline exceeded") // puddle returns ctx error

	assert.EqualValues(t, maxSize, dialCount.Load(), "Dial count should not increase beyond max size")

	// Release one resource
	resources[0].Release()
	assert.EqualValues(t, maxSize-1, pool.pool.Stat().AcquiredResources())
	assert.EqualValues(t, 1, pool.pool.Stat().IdleResources())

	// Try acquiring again, should succeed using the released resource
	acquireCtx2, cancel2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel2()
	res, err := pool.pool.Acquire(acquireCtx2)
	require.NoError(t, err)
	assert.EqualValues(t, maxSize, pool.pool.Stat().AcquiredResources())
	assert.EqualValues(t, 0, pool.pool.Stat().IdleResources())
	assert.EqualValues(t, maxSize, dialCount.Load(), "Dial count should still be max size")

	// Cleanup
	res.Release()
	for i := 1; i < len(resources); i++ {
		resources[i].Release()
	}
	close(blockDial) // Unblock any potentially waiting dialer
}

func TestClientPool_Close(t *testing.T) {
	closeCount := atomic.Int32{}
	dialFunc := func(ctx context.Context, uri string) (*Client, error) {
		mockC := &mockPoolClient{
			closeFunc: func() error {
				closeCount.Add(1)
				return nil
			},
		}
		return NewClient(mockC, mockC), nil
	}
	config := ClientPoolConfig{URI: "mock://", MaxSize: 2}
	pool, cleanup := setupTestPool(t, config, dialFunc)
	// Do not defer cleanup, we call Close explicitly

	// Acquire some clients
	res1, _ := pool.pool.Acquire(context.Background())
	res2, _ := pool.pool.Acquire(context.Background())
	res1.Release() // One idle, one acquired

	assert.EqualValues(t, 1, pool.pool.Stat().IdleResources())
	assert.EqualValues(t, 1, pool.pool.Stat().AcquiredResources())
	assert.EqualValues(t, 2, pool.pool.Stat().TotalResources())

	pool.Close()

	assert.True(t, pool.closed, "Pool should be marked as closed")
	assert.EqualValues(t, 2, closeCount.Load(), "Both clients should be closed")
	assert.EqualValues(t, 0, pool.pool.Stat().TotalResources(), "Pool stats should be zero after close")

	// Try acquiring after close
	_, err := pool.pool.Acquire(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, puddle.ErrClosedPool)

	// Double close should be safe
	pool.Close()
	assert.EqualValues(t, 2, closeCount.Load(), "Close count should not increase on double close")

	// Release acquired resource after pool close (should be handled gracefully by puddle)
	res2.Release()
}

func TestClientPool_Reset(t *testing.T) {
	closeCount := atomic.Int32{}
	dialCount := atomic.Int32{}
	dialFunc := func(ctx context.Context, uri string) (*Client, error) {
		dialCount.Add(1)
		mockC := &mockPoolClient{
			closeFunc: func() error {
				closeCount.Add(1)
				return nil
			},
		}
		return NewClient(mockC, mockC), nil
	}
	config := ClientPoolConfig{URI: "mock://", MaxSize: 2}
	pool, cleanup := setupTestPool(t, config, dialFunc)
	defer cleanup() // Added missing defer

	// Acquire clients
	res1, _ := pool.pool.Acquire(context.Background())
	res2, _ := pool.pool.Acquire(context.Background())
	res1.Release() // One idle, one acquired

	assert.EqualValues(t, 2, dialCount.Load())
	assert.EqualValues(t, 1, pool.pool.Stat().IdleResources())
	assert.EqualValues(t, 1, pool.pool.Stat().AcquiredResources())

	pool.Reset()

	// Reset should destroy idle resources immediately
	assert.EqualValues(t, 1, closeCount.Load(), "Idle client should be closed on Reset")
	assert.EqualValues(t, 0, pool.pool.Stat().IdleResources())
	// Acquired resources are not closed by Reset itself, but marked for destruction on release
	assert.EqualValues(t, 1, pool.pool.Stat().AcquiredResources())
	assert.EqualValues(t, 1, pool.pool.Stat().TotalResources()) // Only acquired left

	// Release the acquired resource - it should now be destroyed
	res2.Release()
	assert.EqualValues(t, 2, closeCount.Load(), "Acquired client should be closed on release after Reset")
	assert.EqualValues(t, 0, pool.pool.Stat().TotalResources(), "Pool should be empty after releasing post-Reset")

	// Acquire again, should dial a new one
	_, err := pool.pool.Acquire(context.Background())
	require.NoError(t, err)
	assert.EqualValues(t, 3, dialCount.Load(), "Should dial a new client after Reset")
}

// Test other call types (Batch, Raw, WithTimeout) briefly, assuming core logic is tested by Call/Notify

func TestClientPool_CallBatch(t *testing.T) {
	callCount := atomic.Int32{}
	mockC := &mockPoolClient{
		callFunc: func(ctx context.Context, rpc any, isNotify bool) (json.RawMessage, error) {
			callCount.Add(1)
			_, ok := rpc.(Batch[*Request])
			require.True(t, ok, "Expected Batch[*Request] type")
			respBatch := NewBatch[*Response](0) // Dummy empty batch response
			raw, _ := json.Marshal(respBatch)
			return raw, nil
		},
		unmarshalFunc: func(data []byte, v any) error {
			return json.Unmarshal(data, v)
		},
	}
	dialFunc := func(ctx context.Context, uri string) (*Client, error) {
		return NewClient(mockC, mockC), nil
	}
	config := ClientPoolConfig{URI: "mock://"}
	pool, cleanup := setupTestPool(t, config, dialFunc)
	defer cleanup()

	req := NewBatch[*Request](0)
	_, err := pool.CallBatch(context.Background(), req)

	require.NoError(t, err)
	assert.EqualValues(t, 1, callCount.Load())
}

func TestClientPool_CallRaw(t *testing.T) {
	callCount := atomic.Int32{}
	mockC := &mockPoolClient{
		callFunc: func(ctx context.Context, rpc any, isNotify bool) (json.RawMessage, error) {
			callCount.Add(1)
			_, ok := rpc.(json.RawMessage) // Raw calls pass json.RawMessage
			require.True(t, ok, "Expected json.RawMessage type")
			respObj := NewResponseWithResult("rawID", "raw_ok")
			raw, _ := json.Marshal(respObj)
			return raw, nil
		},
		unmarshalFunc: func(data []byte, v any) error {
			return json.Unmarshal(data, v)
		},
	}
	dialFunc := func(ctx context.Context, uri string) (*Client, error) {
		return NewClient(mockC, mockC), nil
	}
	config := ClientPoolConfig{URI: "mock://"}
	pool, cleanup := setupTestPool(t, config, dialFunc)
	defer cleanup()

	req := RawRequest(`{}`)
	_, err := pool.CallRaw(context.Background(), req)

	require.NoError(t, err)
	assert.EqualValues(t, 1, callCount.Load())
}

func TestClientPool_CallWithTimeout(t *testing.T) {
	callStarted := make(chan struct{})
	mockC := &mockPoolClient{
		callFunc: func(ctx context.Context, rpc any, isNotify bool) (json.RawMessage, error) {
			close(callStarted)
			select {
			case <-ctx.Done(): // Wait for timeout
				return nil, ctx.Err()
			case <-time.After(1 * time.Second):
				return nil, errors.New("should have timed out")
			}
		},
	}
	dialFunc := func(ctx context.Context, uri string) (*Client, error) {
		return NewClient(mockC, mockC), nil
	}
	config := ClientPoolConfig{URI: "mock://"}
	pool, cleanup := setupTestPool(t, config, dialFunc)
	defer cleanup()

	req := NewRequest(int64(1), "timeoutMethod")
	timeout := 50 * time.Millisecond
	_, err := pool.CallWithTimeout(context.Background(), timeout, req)

	require.Error(t, err)
	// The error comes from the callFunc context check
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}
