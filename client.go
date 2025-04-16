package jsonrpc2

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"
)

// Client represents a client connection to a remote jsonrpc2 server.
// It may be used to make calls against a server and retrieve their responses.
//
// Client is NOT goroutine-safe.
type Client struct {
	e Encoder
	d Decoder
}

// NewClient returns a new [Client] wrapping an the provided [Encoder] and [Decoder].
func NewClient(e Encoder, d Decoder) *Client {
	return &Client{e: e, d: d}
}

// NewClient returns a new [Client] wrapping an the provided [io.ReadWriter].
func NewClientIO(io io.ReadWriter) *Client {
	return &Client{e: NewEncoder(io), d: NewDecoder(io)}
}

// Close closes the underlying [Encoder] and [Decoder] if they support [io.Closer]
//
// Calls to [Client] should not be made after Close has been called.
func (c *Client) Close() error {
	var err error

	if c, ok := c.e.(io.Closer); ok {
		err = c.Close()
	}

	if c, ok := c.d.(io.Closer); ok {
		return errors.Join(err, c.Close())
	}

	return nil
}

func (c *Client) call(ctx context.Context, rpc any, isNotify bool) (json.RawMessage, error) {
	if err := c.e.Encode(ctx, rpc); err != nil {
		return nil, err
	}

	if isNotify {
		return nil, nil
	}

	var resp json.RawMessage
	if err := c.d.Decode(ctx, &resp); err != nil {
		return nil, err
	}

	return resp, nil
}

// Call calls the given [*Request] over the configured stream and returns the [*Response].
func (c *Client) Call(ctx context.Context, r *Request) (*Response, error) {
	rawResp, err := c.call(ctx, r, false)

	if err != nil {
		return nil, err
	}

	var resp Response

	if err := c.d.Unmarshal(rawResp, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

// CallBatch calls a batch of [][*Request] over the configured stream and returns any responses in [][*Response].
func (c *Client) CallBatch(ctx context.Context, r Batch[*Request]) (Batch[*Response], error) {
	rawResp, err := c.call(ctx, r, false)

	if err != nil {
		return nil, err
	}

	resp := NewBatch[*Response](len(r))

	if err := c.d.Unmarshal(rawResp, &resp); err != nil {
		return nil, err
	}

	return resp, nil
}

// CallRaw calls the given [RawRequest] over the configured stream and returns the [*Response].
func (c *Client) CallRaw(ctx context.Context, r RawRequest) (*Response, error) {
	rawResp, err := c.call(ctx, r, false)

	if err != nil {
		return nil, err
	}

	var resp Response

	if err := c.d.Unmarshal(rawResp, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

// CallWithTimeout behaves the same as [Client.Call] but also accepts a timeout for the request.
func (c *Client) CallWithTimeout(ctx context.Context, timeout time.Duration, r *Request) (*Response, error) {
	tctx, stop := context.WithTimeout(ctx, timeout)
	defer stop()

	return c.Call(tctx, r)
}

// CallBatchWithTimeout behaves the same as [Client.CallBatch] but also accepts a timeout for the request.
func (c *Client) CallBatchWithTimeout(ctx context.Context, timeout time.Duration, r Batch[*Request]) (Batch[*Response], error) {
	tctx, stop := context.WithTimeout(ctx, timeout)
	defer stop()

	return c.CallBatch(tctx, r)
}

// CallRawWithTimeout behaves the same as [Client.CallRaw] but also accepts a timeout for the request.
func (c *Client) CallRawWithTimeout(ctx context.Context, timeout time.Duration, r RawRequest) (*Response, error) {
	tctx, stop := context.WithTimeout(ctx, timeout)
	defer stop()

	return c.CallRaw(tctx, r)
}

// Notify sends the given [*Notification], not waiting for a response.
func (c *Client) Notify(ctx context.Context, n *Notification) error {
	if _, err := c.call(ctx, n, true); err != nil {
		return err
	}

	return nil
}

// NotifyBatch sends the given batch of [][*Notifications], not waiting for a response.
func (c *Client) NotifyBatch(ctx context.Context, n Batch[*Notification]) error {
	if _, err := c.call(ctx, n, true); err != nil {
		return err
	}

	return nil
}

// NotifyRaw sends the given [RawNotification], not waiting for a response.
func (c *Client) NotifyRaw(ctx context.Context, n RawNotification) error {
	if _, err := c.call(ctx, n, true); err != nil {
		return err
	}

	return nil
}

// NotifyWithTimeout behaves the same as [Client.Notify] but also accepts a timeout for the notification.
func (c *Client) NotifyWithTimeout(ctx context.Context, timeout time.Duration, n *Notification) error {
	tctx, stop := context.WithTimeout(ctx, timeout)
	defer stop()

	return c.Notify(tctx, n)
}

// NotifyBatchWithTimeout behaves the same as [Client.NotifyBatch] but also accepts a timeout for the notifications.
func (c *Client) NotifyBatchWithTimeout(ctx context.Context, timeout time.Duration, n Batch[*Notification]) error {
	tctx, stop := context.WithTimeout(ctx, timeout)
	defer stop()

	return c.NotifyBatch(tctx, n)
}

// NotifyRawWithTimeout behaves the same as [Client.NotifyRaw] but also accepts a timeout for the notification.
func (c *Client) NotifyRawWithTimeout(ctx context.Context, timeout time.Duration, n RawNotification) error {
	tctx, stop := context.WithTimeout(ctx, timeout)
	defer stop()

	return c.NotifyRaw(tctx, n)
}
