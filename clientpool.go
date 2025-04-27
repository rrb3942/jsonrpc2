package jsonrpc2

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/jackc/puddle/v2"
)

const (
	// DefaultPoolDialTimeout specifies the default timeout (30 seconds) used when establishing
	// a new client connection within the pool. See [ClientPoolConfig.DialTimeout].
	DefaultPoolDialTimeout = 30
	// DefaultPoolIdleTimeout specifies the default timeout (300 seconds or 5 minutes)
	// after which idle client connections in the pool are closed. See [ClientPoolConfig.IdleTimeout].
	DefaultPoolIdleTimeout = 300
)

// ErrRetriesExceeded is returned by ClientPool methods (Call*, Notify*) when an operation
// fails repeatedly due to retryable errors (like network issues) and the configured
// number of retries ([ClientPoolConfig.Retries]) is exhausted. The original error
// that caused the final failure is joined with this error.
var ErrRetriesExceeded = errors.New("retries exceeded")

// ClientPoolConfig holds configuration parameters for creating a [ClientPool].
type ClientPoolConfig struct {
	// URI specifies the target server address (e.g., "tcp:localhost:9090", "http://api.example.com/rpc").
	// This URI is passed to the dialer function when new connections are needed. See [Dial] for supported schemes.
	URI string

	// IdleTimeout defines the maximum duration a client connection can remain idle in the pool
	// before being closed. Defaults to [DefaultPoolIdleTimeout] seconds if zero.
	// A negative value disables idle connection closing.
	IdleTimeout time.Duration

	// DialTimeout specifies the maximum time allowed for establishing a new client connection.
	// Defaults to [DefaultPoolDialTimeout] seconds if zero or negative.
	DialTimeout time.Duration

	// Retries specifies the number of times an operation (Call*, Notify*) should be retried
	// if it fails with a potentially transient error (e.g., network error, EOF).
	// The minimum effective value is 1 (meaning one initial attempt + one retry).
	// Defaults to 1 (one initial attempt + one retry) if zero or negative.
	Retries int

	// MaxSize defines the maximum number of client connections allowed in the pool (both idle and in-use).
	// If zero or negative, it defaults to `min(runtime.NumCPU(), runtime.GOMAXPROCS(-1)) * 2`.
	// Attempts to acquire a connection when the pool is full will block until a connection becomes available
	// or the context is cancelled.
	MaxSize int32

	// AcquireOnCreate, if true, attempts to establish one initial connection when the pool is created.
	// This verifies the URI and dialer configuration early. If the initial connection fails,
	// [NewClientPool] or [NewClientPoolWithDialer] will return an error.
	AcquireOnCreate bool
}

// ClientPool manages a pool of reusable [*TransportClient] connections to a JSON-RPC server.
// It allows multiple goroutines to make concurrent RPC calls efficiently by reusing
// established connections. It also handles automatic retries for certain types of errors
// and manages the lifecycle (creation, closing) of client connections.
//
// Use [NewClientPool] or [NewClientPoolWithDialer] to create instances.
type ClientPool struct {
	pool    *puddle.Pool[*TransportClient] // Underlying connection pool from github.com/jackc/puddle/v2
	idle    *time.Timer                    // Timer for closing idle connections
	retries int                            // Number of allowed attempts (initial + retries)
	closed  bool                           // Flag indicating if Close() has been called
	mu      sync.Mutex                     // Protects access to closed flag and idle timer
}

// NewClientPool creates a new [ClientPool] using the default [DialTransport] function.
// It connects to the server specified in [ClientPoolConfig.URI].
//
// If [ClientPoolConfig.AcquireOnCreate] is true, it attempts to establish an initial
// connection and returns an error if it fails.
//
// Example:
//
//	config := jsonrpc2.ClientPoolConfig{
//	    URI:         "tcp:localhost:5000",
//	    MaxSize:     10,
//	    IdleTimeout: 5 * time.Minute,
//	    Retries:     2, // Initial attempt + 2 retries = 3 total attempts
//	}
//	pool, err := jsonrpc2.NewClientPool(context.Background(), config)
//	if err != nil {
//	    log.Fatalf("Failed to create client pool: %v", err)
//	}
//	defer pool.Close()
//	// Use pool.Call(), pool.Notify(), etc.
func NewClientPool(nctx context.Context, config ClientPoolConfig) (*ClientPool, error) {
	// Delegates to NewClientPoolWithDialer using the default DialTransport function.
	return NewClientPoolWithDialer(nctx, config, DialTransport)
}

// NewClientPoolWithDialer creates a new [ClientPool] using a custom dialer function.
// This allows using non-standard transports or custom connection logic. The `dialFunc`
// should establish a connection to the server specified by `config.URI` and return
// a [*TransportClient].
//
// If [ClientPoolConfig.AcquireOnCreate] is true, it attempts to establish an initial
// connection using the provided `dialFunc` and returns an error if it fails.
//
// Example (using a custom dialer for TLS with specific config):
//
//	customTLSConfig := &tls.Config{ /* ... */ }
//	customDialer := func(ctx context.Context, uri string) (*jsonrpc2.Client, error) {
//	    // Example: Parse URI, dial with custom TLS config
//	    // urlInfo, _ := url.Parse(uri) ... addr := urlInfo.Host ...
//	    addr := "secure.example.com:443" // Simplified for example
//	    dialer := &tls.Dialer{Config: customTLSConfig}
//	    conn, err := dialer.DialContext(ctx, "tcp", addr)
//	    if err != nil {
//	        return nil, err
//	    }
//	    return jsonrpc2.NewClientIO(conn), nil
//	}
//
//	config := jsonrpc2.ClientPoolConfig{ URI: "tls://secure.example.com:443", /* ... */ }
//	pool, err := jsonrpc2.NewClientPoolWithDialer(context.Background(), config, customDialer)
//	// ... handle error and use pool ...
func NewClientPoolWithDialer(nctx context.Context, config ClientPoolConfig, dialFunc func(ctx context.Context, uri string) (*TransportClient, error)) (*ClientPool, error) {
	// Set defaults if zero values provided.
	if config.IdleTimeout == 0 {
		config.IdleTimeout = time.Duration(DefaultPoolIdleTimeout) * time.Second
	}

	if config.DialTimeout <= 0 {
		config.DialTimeout = time.Duration(DefaultPoolDialTimeout) * time.Second
	}

	if config.MaxSize <= 0 {
		//nolint:gosec,mnd //How many cpus do you think we have? Puddle requires int32.
		config.MaxSize = int32(min(runtime.NumCPU(), runtime.GOMAXPROCS(-1)) * 2)
	}

	pool, err := puddle.NewPool[*TransportClient](&puddle.Config[*TransportClient]{
		Constructor: func(ctx context.Context) (*TransportClient, error) {
			dialCtx, stop := context.WithTimeout(ctx, config.DialTimeout)
			defer stop()
			return dialFunc(dialCtx, config.URI)
		},
		Destructor: func(client *TransportClient) { _ = client.Close() },
		MaxSize:    config.MaxSize,
	})

	if err != nil {
		return nil, err
	}

	if config.AcquireOnCreate {
		res, err := pool.Acquire(nctx)

		if err != nil {
			defer pool.Close()
			return nil, err
		}

		defer res.Release()
	}

	cpool := &ClientPool{pool: pool}
	// Ensure at least one retry attempt beyond the initial try.
	// The loop logic uses `range cp.retries`, so retries=1 means 2 total attempts.
	cpool.retries = max(config.Retries, 1) + 1 // config.Retries=1 -> 2 attempt; config.Retries=2 -> 3 attempts etc.

	// Setup idle connection cleanup if IdleTimeout is positive.
	if config.IdleTimeout > 0 {
		cpool.idle = time.AfterFunc(config.IdleTimeout, func() { // Initial delay
			cpool.mu.Lock()
			defer cpool.mu.Unlock()

			// Check if pool was closed while waiting for the lock or timer.

			if cpool.closed {
				return
			}

			nextWait := config.IdleTimeout

			for _, res := range cpool.pool.AcquireAllIdle() {
				idleTime := res.IdleDuration()
				if idleTime >= config.IdleTimeout {
					res.Destroy()
				} else {
					nextWait = min(nextWait, config.IdleTimeout-idleTime)
				}
			}

			cpool.idle.Reset(nextWait)
		})
	}

	return cpool, nil
}

// Close gracefully shuts down the client pool.
// It stops the idle connection cleanup timer, closes the underlying pool,
// closes all idle connections, and waits for any acquired connections
// to be released before closing them.
//
// After Close returns, the pool should not be used. Calling methods on a closed
// pool will likely result in errors.
// It is safe to call Close multiple times.
func (cp *ClientPool) Close() {
	cp.mu.Lock()
	if cp.closed {
		cp.mu.Unlock()
		return // Already closed
	}

	cp.closed = true

	// Stop the idle timer goroutine if it exists.
	if cp.idle != nil {
		cp.idle.Stop()
	}
	cp.mu.Unlock() // Unlock before closing the pool, as pool.Close() might block.

	// Close the underlying puddle pool. This handles closing connections.
	cp.pool.Close()
}

// Reset closes all idle connections in the pool and marks all currently acquired
// connections to be closed when they are released. Any subsequent Acquire calls
// will create new connections.
// This is useful for forcing connections to be re-established, for example, after
// a network configuration change or suspected server issue.
func (cp *ClientPool) Reset() {
	cp.pool.Reset()
}

// releaseMaybeRetry is an internal helper function to manage the lifecycle of a
// puddle resource ([*puddle.Resource[*TransportClient]]) after a call attempt.
// It decides whether the error warrants destroying the client connection and
// potentially retrying the operation with a new connection.
func releaseMaybeRetry(res *puddle.Resource[*TransportClient], err error) (needsRetry bool) {
	if err != nil {
		// Always destroy the connection resource on any error during a call.
		// This ensures potentially corrupted connections are not reused.
		res.Destroy()

		// Check if the error is one that suggests retrying might succeed
		// (e.g., network errors, unexpected EOF).
		// Context errors (canceled, deadline exceeded) are not retryable.
		switch {
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return false // Do not retry context errors
		case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, net.ErrClosed), errors.Is(err, os.ErrClosed):
			// These errors often indicate a broken connection; retrying with a new one might work.
			return true
		}

		// Return here so we dont release a destroyed
		return false
	}

	// No error occurred, release the resource back to the pool for reuse.
	res.Release()

	return false // No retry needed
}

// Call acquires a client connection from the pool, sends the request using [Client.Call],
// and returns the response.
// If a retryable error occurs (e.g., network issue), it automatically retries the call
// with a new connection up to the configured number of retries ([ClientPoolConfig.Retries]).
// If retries are exhausted, it returns [ErrRetriesExceeded] joined with the last error encountered.
//
// Example:
//
//	req := jsonrpc2.NewRequest(int64(1), "myMethod")
//	resp, err := pool.Call(context.Background(), req)
//	if err != nil {
//	    if errors.Is(err, jsonrpc2.ErrRetriesExceeded) {
//	        log.Printf("Call failed after multiple retries: %v", err)
//	    } else {
//	        log.Printf("Call failed: %v", err)
//	    }
//	    return
//	}
//	// Process successful response...
func (cp *ClientPool) Call(ctx context.Context, req *Request) (resp *Response, err error) {
	// Loop for the configured number of attempts (initial + retries).
	for range cp.retries {
		// Check context cancellation before acquiring a resource.
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr // Return context error immediately
		}

		client, cerr := cp.pool.Acquire(ctx)

		if cerr != nil {
			return nil, cerr
		}

		resp, err = client.Value().Call(ctx, req)

		if needsRetry := releaseMaybeRetry(client, err); needsRetry {
			continue
		}

		return resp, err
	}

	// If all retries failed, join ErrRetriesExceeded with the last error.
	return nil, errors.Join(ErrRetriesExceeded, err)
}

// CallBatch acquires a client connection from the pool, sends the batch request
// using [Client.CallBatch], and returns the batch response.
// Handles retries similarly to [ClientPool.Call].
// Returns [ErrRetriesExceeded] if retries are exhausted.
func (cp *ClientPool) CallBatch(ctx context.Context, req Batch[*Request]) (resp Batch[*Response], err error) {
	for range cp.retries {
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}

		client, cerr := cp.pool.Acquire(ctx)

		if cerr != nil {
			return nil, cerr
		}

		resp, err = client.Value().CallBatch(ctx, req)

		if needsRetry := releaseMaybeRetry(client, err); needsRetry {
			continue
		}

		return resp, err
	}

	return nil, errors.Join(ErrRetriesExceeded, err)
}

// CallRaw acquires a client connection from the pool, sends the raw request
// using [Client.CallRaw], and returns the response.
// Handles retries similarly to [ClientPool.Call].
// Returns [ErrRetriesExceeded] if retries are exhausted.
func (cp *ClientPool) CallRaw(ctx context.Context, req RawRequest) (resp *Response, err error) {
	for range cp.retries {
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}

		client, cerr := cp.pool.Acquire(ctx)

		if cerr != nil {
			return nil, cerr
		}

		resp, err = client.Value().CallRaw(ctx, req)

		if needsRetry := releaseMaybeRetry(client, err); needsRetry {
			continue
		}

		return resp, err
	}

	return nil, errors.Join(ErrRetriesExceeded, err)
}

// CallWithTimeout is a convenience method that calls [ClientPool.Call] with a derived context
// that includes the specified `timeout`.
func (cp *ClientPool) CallWithTimeout(ctx context.Context, timeout time.Duration, r *Request) (*Response, error) {
	tctx, stop := context.WithTimeout(ctx, timeout)
	defer stop()

	return cp.Call(tctx, r)
}

// CallBatchWithTimeout is a convenience method that calls [ClientPool.CallBatch] with a derived context
// that includes the specified `timeout`.
func (cp *ClientPool) CallBatchWithTimeout(ctx context.Context, timeout time.Duration, r Batch[*Request]) (Batch[*Response], error) {
	tctx, stop := context.WithTimeout(ctx, timeout)
	defer stop()

	return cp.CallBatch(tctx, r)
}

// CallRawWithTimeout is a convenience method that calls [ClientPool.CallRaw] with a derived context
// that includes the specified `timeout`.
func (cp *ClientPool) CallRawWithTimeout(ctx context.Context, timeout time.Duration, r RawRequest) (*Response, error) {
	tctx, stop := context.WithTimeout(ctx, timeout)
	defer stop()

	return cp.CallRaw(tctx, r)
}

// Notify acquires a client connection from the pool and sends the notification
// using [Client.Notify]. Since notifications don't receive responses, this method
// only returns errors related to acquiring a connection or sending the notification.
// Handles retries similarly to [ClientPool.Call].
// Returns [ErrRetriesExceeded] if retries are exhausted.
//
// Example:
//
//	notif := jsonrpc2.NewNotificationWithParams("logEvent", NewParamsObject(map[string]any{"level": "warn", "msg": "Disk space low"}))
//	err := pool.Notify(context.Background(), notif)
//	if err != nil {
//	    log.Printf("Notify failed: %v", err)
//	}
func (cp *ClientPool) Notify(ctx context.Context, notify *Notification) (err error) {
	for range cp.retries {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}

		client, cerr := cp.pool.Acquire(ctx)

		if cerr != nil {
			return cerr
		}

		err = client.Value().Notify(ctx, notify)

		if needsRetry := releaseMaybeRetry(client, err); needsRetry {
			continue
		}

		return err
	}

	return errors.Join(ErrRetriesExceeded, err)
}

// NotifyBatch acquires a client connection from the pool and sends the batch notification
// using [Client.NotifyBatch].
// Handles retries similarly to [ClientPool.Notify].
// Returns [ErrRetriesExceeded] if retries are exhausted.
func (cp *ClientPool) NotifyBatch(ctx context.Context, notify Batch[*Notification]) (err error) {
	for range cp.retries {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}

		client, cerr := cp.pool.Acquire(ctx)

		if cerr != nil {
			return cerr
		}

		err = client.Value().NotifyBatch(ctx, notify)

		if needsRetry := releaseMaybeRetry(client, err); needsRetry {
			continue
		}

		return err
	}

	return errors.Join(ErrRetriesExceeded, err)
}

// NotifyRaw acquires a client connection from the pool and sends the raw notification
// using [Client.NotifyRaw].
// Handles retries similarly to [ClientPool.Notify].
// Returns [ErrRetriesExceeded] if retries are exhausted.
func (cp *ClientPool) NotifyRaw(ctx context.Context, notify RawNotification) (err error) {
	for range cp.retries {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}

		client, cerr := cp.pool.Acquire(ctx)

		if cerr != nil {
			return cerr
		}

		err = client.Value().NotifyRaw(ctx, notify)

		if needsRetry := releaseMaybeRetry(client, err); needsRetry {
			continue
		}

		return err
	}

	return errors.Join(ErrRetriesExceeded, err)
}

// NotifyWithTimeout is a convenience method that calls [ClientPool.Notify] with a derived context
// that includes the specified `timeout`.
func (cp *ClientPool) NotifyWithTimeout(ctx context.Context, timeout time.Duration, n *Notification) error {
	tctx, stop := context.WithTimeout(ctx, timeout)
	defer stop()

	return cp.Notify(tctx, n)
}

// NotifyBatchWithTimeout is a convenience method that calls [ClientPool.NotifyBatch] with a derived context
// that includes the specified `timeout`.
func (cp *ClientPool) NotifyBatchWithTimeout(ctx context.Context, timeout time.Duration, n Batch[*Notification]) error {
	tctx, stop := context.WithTimeout(ctx, timeout)
	defer stop()

	return cp.NotifyBatch(tctx, n)
}

// NotifyRawWithTimeout is a convenience method that calls [ClientPool.NotifyRaw] with a derived context
// that includes the specified `timeout`.
func (cp *ClientPool) NotifyRawWithTimeout(ctx context.Context, timeout time.Duration, n RawNotification) error {
	tctx, stop := context.WithTimeout(ctx, timeout)
	defer stop()

	return cp.NotifyRaw(tctx, n)
}
