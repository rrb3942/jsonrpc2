package jsonrpc2

import (
	"context"
	"sync/atomic"
	"time"
)

// BasicClient provides a simplified interface for making JSON-RPC 2.0 calls
// to a server. It wraps a [ClientPool] (configured with MaxSize=1) to manage
// the underlying connection and automatically handles request IDs.
//
// It does not support sending batch requests. Use [Client] or [ClientPool] directly
// for batching capabilities.
//
// Use [DialBasic] to create instances connected to a server URI.
//
// BasicClient is goroutine-safe.
type BasicClient struct {
	pool           *ClientPool
	id             atomic.Uint32 // Use atomic type for thread-safe ID generation.
	defaultTimeout time.Duration
}

// SetDefaultTimeout sets a default timeout duration for all subsequent Call and Notify
// operations made through this BasicClient. If the duration `d` is greater than zero,
// a context with this timeout will be derived from the context passed to Call/Notify.
// If `d` is zero or negative, no default timeout is applied, and the original context's
// deadline (if any) is used.
func (c *BasicClient) SetDefaultTimeout(d time.Duration) {
	c.defaultTimeout = d
}

// Close closes the underlying connection pool.
// Calls to the BasicClient should not be made after Close has been called.
func (c *BasicClient) Close() {
	c.pool.Close()
}

// Call sends a JSON-RPC request for the given method with the provided parameters.
// It automatically assigns a request ID and waits for the server's response.
// If a default timeout is set via SetDefaultTimeout, it will be applied to the call.
// Returns the server's [*Response] or an error if the call fails (including potential
// retries managed by the underlying pool).
func (c *BasicClient) Call(ctx context.Context, method string, params Params) (*Response, error) {
	// Apply default timeout if set
	if c.defaultTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.defaultTimeout)
		defer cancel()
	}

	// Atomically increment and get the next ID.
	// Note: While the pool handles concurrency, atomic ID ensures uniqueness
	// if multiple goroutines use the *same* BasicClient instance, matching original intent.
	// However, with pool size 1, this is less critical. Consider removing if simplifying further.
	// For now, keep it for behavioral consistency.
	// TODO: Re-evaluate atomic ID necessity with pool size 1.
	nextID := c.id.Add(1) // Use Add method of atomic.Uint32

	req := NewRequestWithParams(int64(nextID), method, params)
	return c.pool.Call(ctx, req)
}

// Notify sends a JSON-RPC notification for the given method with the provided parameters.
// It does not wait for a server response.
// If a default timeout is set via SetDefaultTimeout, it will be applied to the notification attempt.
// Returns an error only if sending the notification fails (including potential retries).
func (c *BasicClient) Notify(ctx context.Context, method string, params Params) error {
	// Apply default timeout if set
	if c.defaultTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.defaultTimeout)
		defer cancel()
	}

	notif := NewNotificationWithParams(method, params)
	return c.pool.Notify(ctx, notif)
}
