package jsonrpc2

import (
	"context"
	"io"
	"math/rand/v2"
	"time"
)

// BasicClient represents a client connection to a remote jsonrpc2 server.
// It may be used to make calls against a server and retrieve their responses.
//
// It offers a simplified API that internally manages [ID], with no ability to send batches.
//
// If [Params] are empty (Params{}), they are not included in the request/notification.
//
// BasicClient is NOT goroutine-safe.
type BasicClient struct {
	client *Client
	id     uint32
}

// NewBasicClient returns a new [BasicClient] wrapping an the provided [Encoder] and [Decoder].
func NewBasicClient(e Encoder, d Decoder) *BasicClient {
	//nolint:gosec // We just want to avoid always starting at 0
	return &BasicClient{client: NewClient(e, d), id: rand.Uint32()}
}

// NewBasicClient returns a new [BasicClient] wrapping an the provided [io.ReadWriter].
func NewBasicClientIO(io io.ReadWriter) *BasicClient {
	//nolint:gosec // We just want to avoid always starting at 0
	return &BasicClient{client: NewClientIO(io), id: rand.Uint32()}
}

// Close closes the underlying [Client]
//
// Calls to [BasicClient] should not be made after Close has been called.
func (c *BasicClient) Close() error {
	return c.client.Close()
}

// Call calls the given method over the configured stream and returns the [*Response].
func (c *BasicClient) Call(ctx context.Context, method string, params Params) (*Response, error) {
	id := c.id
	id++

	return c.client.Call(ctx, NewRequestWithParams(int64(id), method, params))
}

// CallWithTimeout behaves the same as [BasicClient.Call] but also accepts a timeout for the request.
func (c *BasicClient) CallWithTimeout(ctx context.Context, timeout time.Duration, method string, params Params) (*Response, error) {
	id := c.id
	id++

	return c.client.CallWithTimeout(ctx, timeout, NewRequestWithParams(int64(id), method, params))
}

// Notify sends the given method as a notification, not waiting for a response.
func (c *BasicClient) Notify(ctx context.Context, method string, params Params) error {
	return c.client.Notify(ctx, NewNotificationWithParams(method, params))
}

// NotifyWithTimeout behaves the same as [BasicClient.Notify] but also accepts a timeout for the notification.
func (c *BasicClient) NotifyWithTimeout(ctx context.Context, timeout time.Duration, method string, params Params) error {
	return c.client.NotifyWithTimeout(ctx, timeout, NewNotificationWithParams(method, params))
}
