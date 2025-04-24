package jsonrpc2

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/jackc/puddle/v2"
)

const (
	// Default timeout in seconds when the pool dials a new client.
	DefaultPoolDialTimeout = 30
	// Default timeout in seconds for cleaning idle clients.
	DefaultPoolIdleTimeout = 300
)

// ErrRetriesExceeded is returned when the client pool exceeded its retry budget.
var ErrRetriesExceeded = errors.New("retries exceeded")

// ClientPoolConfig contains all the configuration options used when creating a [*ClientPool].
type ClientPoolConfig struct {
	// Remote URI to connect to.
	URI string
	// Timeout for closing idle connections. Defaults to [DefaultPooLidleTimeout] seconds. Negative disables.
	IdleTimeout time.Duration
	// Timeout used when connection new clients. Defaults to [DefaultPoolDialTimeout] seconds. Must be positive.
	DialTimeout time.Duration
	// Retries for IO errors. Cannot be less than 1, to handle stale clients.
	Retries int
	// Max number of client connections.
	MaxSize int32
	// Force Acquiring on creation to verify configuration.
	AcquireOnCreate bool
}

// ClientPool is a managed pool of [*Clients] that allows for concurrent calls.
type ClientPool struct {
	pool    *puddle.Pool[*Client]
	idle    *time.Timer
	retries int
	closed  bool
	mu      sync.Mutex
}

// NewClientPool returns a new [*ClientPool] ready for use.
//
// If [ClientPooLConfig.AcquireOnCreate] is enabled, an error will be returned if an initial
// acquire fails.
func NewClientPool(nctx context.Context, config ClientPoolConfig) (*ClientPool, error) {
	return NewClientPoolWithDialer(nctx, config, Dial)
}

// NewClientPoolWithDialer returns a new [*ClientPool] ready for use with a custom dialer.
//
// A custom dialer may be used to support custom transports.
//
// If [ClientPooLConfig.AcquireOnCreate] is enabled, an error will be returned if an initial
// acquire fails.
func NewClientPoolWithDialer(nctx context.Context, config ClientPoolConfig, dialFunc func(ctx context.Context, uri string) (*Client, error)) (*ClientPool, error) {
	if config.IdleTimeout == 0 {
		config.IdleTimeout = time.Duration(DefaultPoolIdleTimeout) * time.Second
	}

	if config.DialTimeout <= 0 {
		config.DialTimeout = time.Duration(DefaultPoolDialTimeout) * time.Second
	}

	pool, err := puddle.NewPool[*Client](&puddle.Config[*Client]{
		Constructor: func(ctx context.Context) (*Client, error) {
			dialCtx, stop := context.WithTimeout(ctx, config.DialTimeout)
			defer stop()
			return dialFunc(dialCtx, config.URI)
		},
		Destructor: func(client *Client) { _ = client.Close() },
		MaxSize:    config.MaxSize,
	})

	if err != nil {
		return nil, err
	}

	if config.AcquireOnCreate {
		if _, err := pool.Acquire(nctx); err != nil {
			defer pool.Close()
			return nil, err
		}
	}

	cpool := &ClientPool{pool: pool}
	cpool.retries = max(config.Retries, 1)
	cpool.retries++

	if config.IdleTimeout > 0 {
		cpool.idle = time.AfterFunc(config.IdleTimeout, func() {
			cpool.mu.Lock()
			defer cpool.mu.Unlock()

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

// Close closes a pool and all underlying clients.
// After a call to Close the pool should not be used again.
func (cp *ClientPool) Close() {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	cp.closed = true

	if cp.idle != nil {
		cp.idle.Stop()
	}

	cp.pool.Close()
}

// Reset will reset the underlying pool, removing all current connections allowing for new ones to be created.
func (cp *ClientPool) Reset() {
	cp.pool.Reset()
}

func releaseMaybeRetry(res *puddle.Resource[*Client], err error) (needsRetry bool) {
	// Errors that need special handling
	if err != nil {
		// Because of error wrapping its better to just special case some handling
		switch {
		// Context was cancelled, by may not be a timeout so client is still OK
		case errors.Is(err, context.Canceled):
			res.Release()
			return false
		// Timeout, destroy the handle
		case errors.Is(err, context.DeadlineExceeded):
			res.Destroy()
			return false
		// Broken stream, try a new handle
		case errors.Is(err, io.EOF), errors.Is(err, net.ErrClosed), errors.Is(err, os.ErrClosed):
			res.Destroy()
			return true
		}
	}

	res.Release()

	return false
}

// Call acquires a client from the pool and performs a [Client.Call].
func (cp *ClientPool) Call(ctx context.Context, req *Request) (resp *Response, err error) {
	for range cp.retries {
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
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

	return nil, errors.Join(ErrRetriesExceeded, err)
}

// CallBatch acquires a client from the pool and performs a [Client.CallBatch].
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

// CallRaw acquires a client from the pool and performs a [Client.CallRaw].
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

// CallWithTimeout acquires a client from the pool and performs a [Client.CallWithTimeout].
func (cp *ClientPool) CallWithTimeout(ctx context.Context, timeout time.Duration, r *Request) (*Response, error) {
	tctx, stop := context.WithTimeout(ctx, timeout)
	defer stop()

	return cp.Call(tctx, r)
}

// CallBatchWithTimeout acquires a client from the pool and performs a [Client.CallBatchWithTimeout].
func (cp *ClientPool) CallBatchWithTimeout(ctx context.Context, timeout time.Duration, r Batch[*Request]) (Batch[*Response], error) {
	tctx, stop := context.WithTimeout(ctx, timeout)
	defer stop()

	return cp.CallBatch(tctx, r)
}

// CallRawWithTimeout acquires a client from the pool and performs a [Client.CallRawWithTimeout].
func (cp *ClientPool) CallRawWithTimeout(ctx context.Context, timeout time.Duration, r RawRequest) (*Response, error) {
	tctx, stop := context.WithTimeout(ctx, timeout)
	defer stop()

	return cp.CallRaw(tctx, r)
}

// Notify acquires a client from the pool and performs a [Client.Notify].
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

// NotifyBatch acquires a client from the pool and performs a [Client.NotifyBatch].
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

// NotifyRaw acquires a client from the pool and performs a [Client.NotifyRaw].
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

// NotifyWithTimeout acquires a client from the pool and performs a [Client.NotifyWithTimeout].
func (cp *ClientPool) NotifyWithTimeout(ctx context.Context, timeout time.Duration, n *Notification) error {
	tctx, stop := context.WithTimeout(ctx, timeout)
	defer stop()

	return cp.Notify(tctx, n)
}

// NotifyBatchWithTimeout acquires a client from the pool and performs a [Client.NotifyBatchWithTimeout].
func (cp *ClientPool) NotifyBatchWithTimeout(ctx context.Context, timeout time.Duration, n Batch[*Notification]) error {
	tctx, stop := context.WithTimeout(ctx, timeout)
	defer stop()

	return cp.NotifyBatch(tctx, n)
}

// NotifyRawWithTimeout acquires a client from the pool and performs a [Client.NotifyRawWithTimeout].
func (cp *ClientPool) NotifyRawWithTimeout(ctx context.Context, timeout time.Duration, n RawNotification) error {
	tctx, stop := context.WithTimeout(ctx, timeout)
	defer stop()

	return cp.NotifyRaw(tctx, n)
}
