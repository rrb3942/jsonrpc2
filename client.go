package jsonrpc2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

// ErrInvalidParamsType indicates that the provided params argument to Client.Call or Client.Notify
// did not marshal into a JSON object or array as required by the JSON-RPC 2.0 specification.
var ErrInvalidParamsType = errors.New("jsonrpc2: params must marshal to a JSON object or array")

// makeParamsFromAny marshals the given value and validates that it represents
// a JSON object or array, returning a Params struct or an error.
// If v is nil, it returns empty Params{} and no error, signifying omitted parameters.
func makeParamsFromAny(v any) (Params, error) {
	if v == nil {
		// If the input is nil, treat it as omitted parameters (valid in JSON-RPC 2.0).
		// Return the zero value for Params and no error.
		return Params{}, nil
	}

	// Marshal the non-nil value to JSON bytes.
	raw, err := Marshal(v)
	if err != nil {
		return Params{}, fmt.Errorf("jsonrpc2: failed to marshal params: %w", err)
	}

	hint := jsonHintType(raw)
	if hint != TypeObject && hint != TypeArray {
		return Params{}, fmt.Errorf("%w: got %T", ErrInvalidParamsType, v)
	}

	return NewParamsRaw(json.RawMessage(raw)), nil
}

// Client provides a simplified interface for making JSON-RPC 2.0 calls
// to a server. It wraps a [ClientPool] to manage
// the underlying connection and automatically handles request IDs.
//
// Use [Dial] to create instances connected to a server URI.
//
// poolClientInterface defines the subset of ClientPool methods used by Client.
// This allows mocking the pool for testing.
type poolClientInterface interface {
	Call(ctx context.Context, req *Request) (*Response, error)
	CallWithTimeout(ctx context.Context, timeout time.Duration, req *Request) (*Response, error)
	Notify(ctx context.Context, notif *Notification) error
	NotifyWithTimeout(ctx context.Context, timeout time.Duration, notif *Notification) error
	CallBatch(ctx context.Context, batch Batch[*Request]) (Batch[*Response], error)
	CallBatchWithTimeout(ctx context.Context, timeout time.Duration, batch Batch[*Request]) (Batch[*Response], error)
	NotifyBatch(ctx context.Context, batch Batch[*Notification]) error
	NotifyBatchWithTimeout(ctx context.Context, timeout time.Duration, batch Batch[*Notification]) error
	Close()
}

// Client is goroutine-safe.
type Client struct {
	pool           poolClientInterface // Use interface for testability
	id             atomic.Uint32       // Use atomic type for thread-safe ID generation.
	defaultTimeout time.Duration
}

// Small wrapper to help get next ID value.
func (c *Client) nextID() int64 {
	return int64(c.id.Add(1))
}

// SetDefaultTimeout sets a default timeout duration for all subsequent Call and Notify
// operations made through this Client. If the duration `d` is greater than zero,
// a context with this timeout will be derived from the context passed to Call/Notify.
// If `d` is zero or negative, no default timeout is applied, and the original context's
// deadline (if any) is used.
func (c *Client) SetDefaultTimeout(d time.Duration) {
	c.defaultTimeout = d
}

// Close closes the underlying connection pool.
// Calls to the Client should not be made after Close has been called.
func (c *Client) Close() {
	c.pool.Close()
}

// Call sends a JSON-RPC request for the given method with the provided parameters.
// It automatically assigns a request ID and waits for the server's response.
// If a default timeout is set via SetDefaultTimeout, it will be applied to the call.
// The `params` argument can be any Go type that marshals to a JSON object or array,
// or nil to omit parameters. If `params` marshals to anything else, an error
// wrapping [ErrInvalidParamsType] is returned.
// Returns the server's [*Response] or an error if the call fails (including potential
// retries managed by the underlying pool).
func (c *Client) Call(ctx context.Context, method string, params any) (*Response, error) {
	// Validate and convert params
	reqParams, err := makeParamsFromAny(params)
	if err != nil {
		return nil, err
	}

	req := NewRequestWithParams(c.nextID(), method, reqParams)

	// Call the appropriate pool method based on whether a default timeout is set.
	if c.defaultTimeout > 0 {
		return c.pool.CallWithTimeout(ctx, c.defaultTimeout, req)
	}

	return c.pool.Call(ctx, req)
}

// Notify sends a JSON-RPC notification for the given method with the provided parameters.
// It does not wait for a server response.
// If a default timeout is set via SetDefaultTimeout, it will be applied to the notification attempt.
// The `params` argument can be any Go type that marshals to a JSON object or array,
// or nil to omit parameters. If `params` marshals to anything else, an error
// wrapping [ErrInvalidParamsType] is returned.
// Returns an error only if sending the notification fails (including potential retries).
func (c *Client) Notify(ctx context.Context, method string, params any) error {
	// Validate and convert params
	notifyParams, err := makeParamsFromAny(params)
	if err != nil {
		return err
	}

	notif := NewNotificationWithParams(method, notifyParams)

	// Call the appropriate pool method based on whether a default timeout is set.
	if c.defaultTimeout > 0 {
		return c.pool.NotifyWithTimeout(ctx, c.defaultTimeout, notif)
	}

	return c.pool.Notify(ctx, notif)
}

// NewRequestBatch creates a new [BatchBuilder] for constructing a batch of JSON-RPC requests.
// Requests added via [*BatchBuilder.Add] will automatically receive unique IDs.
// The `size` parameter provides a hint for the initial capacity of the underlying batch slice.
// The batch is sent using [*BatchBuilder.Call].
func (c *Client) NewRequestBatch(size int) *BatchBuilder[*Request] {
	return &BatchBuilder[*Request]{parent: c, Batch: NewBatch[*Request](size)}
}

// NewNotificationBatch creates a new [BatchBuilder] for constructing a batch of JSON-RPC notifications.
// Notifications added via [*BatchBuilder.Add] will not have IDs.
// The `size` parameter provides a hint for the initial capacity of the underlying batch slice.
// The batch is sent using [*BatchBuilder.Call].
func (c *Client) NewNotificationBatch(size int) *BatchBuilder[*Notification] {
	return &BatchBuilder[*Notification]{parent: c, Batch: NewBatch[*Notification](size)}
}

// callBatch is an internal helper used by BatchBuilder to send request batches via the client's pool.
// It applies the client's default timeout if configured.
func (c *Client) callBatch(ctx context.Context, batch Batch[*Request]) (Batch[*Response], error) {
	if c.defaultTimeout > 0 {
		return c.pool.CallBatchWithTimeout(ctx, c.defaultTimeout, batch)
	}

	return c.pool.CallBatch(ctx, batch)
}

// notifyBatch is an internal helper used by BatchBuilder to send notification batches via the client's pool.
// It applies the client's default timeout if configured.
func (c *Client) notifyBatch(ctx context.Context, batch Batch[*Notification]) error {
	if c.defaultTimeout > 0 {
		return c.pool.NotifyBatchWithTimeout(ctx, c.defaultTimeout, batch)
	}

	return c.pool.NotifyBatch(ctx, batch)
}
