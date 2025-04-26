package jsonrpc2

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"
)

// Client provides methods for making JSON-RPC 2.0 calls to a remote server
// over a single underlying connection or stream.
//
// A Client instance manages encoding requests and decoding responses.
// It is goroutine-safe, meaning multiple goroutines can make calls concurrently
// using the same Client instance. However, all calls are serialized over the
// single underlying transport managed by the associated [Encoder] and [Decoder].
// For concurrent connections, consider using a [ClientPool].
//
// Use [NewClient] or [NewClientIO] to create instances.
type Client struct {
	e  Encoder
	d  Decoder
	mu sync.Mutex // Protects concurrent access to Encode/Decode operations on the shared stream.
}

// NewClient creates a new [Client] that uses the provided [Encoder] and [Decoder]
// for communication. This allows using custom encoding/decoding logic or transports.
func NewClient(e Encoder, d Decoder) *Client {
	return &Client{e: e, d: d}
}

// NewClientIO creates a new [Client] that communicates over the given [io.ReadWriter].
// It wraps the `rw` with the default [NewEncoder] and [NewDecoder] implementations.
// This is a convenient way to create a client for standard network connections ([net.Conn])
// or other stream-based transports.
func NewClientIO(rw io.ReadWriter) *Client {
	return &Client{e: NewEncoder(rw), d: NewDecoder(rw)}
}

// Close attempts to close the underlying [Encoder] and [Decoder] if they implement
// the [io.Closer] interface. It ensures that closing happens safely even if called
// concurrently with other operations.
//
// It is safe to call Close multiple times; subsequent calls after the first
// will have no effect and return nil (unless the underlying closer returns an error
// on subsequent calls, which is atypical).
//
// After Close is called, the Client should no longer be used for making calls,
// as the underlying transport will likely be closed. Errors from closing both
// the encoder and decoder (if applicable) are joined using [errors.Join].
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var err error

	if c, ok := c.e.(io.Closer); ok {
		err = c.Close()
	}

	if c, ok := c.d.(io.Closer); ok {
		return errors.Join(err, c.Close())
	}

	return err
}

// call is the internal method handling the core logic of sending a request/notification
// and potentially reading a response. It acquires the client's mutex to ensure
// serialized access to the underlying encoder/decoder.
func (c *Client) call(ctx context.Context, rpc any, isNotify bool) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check context cancellation *before* encoding.
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Encode and send the request/notification.
	if err := c.e.Encode(ctx, rpc); err != nil {
		return nil, err
	}

	// If it's a notification, we don't expect or wait for a response.
	if isNotify {
		return nil, nil
	}

	// Decode the response.
	var resp json.RawMessage
	if err := c.d.Decode(ctx, &resp); err != nil {
		return nil, err
	}

	return resp, nil
}

// Call sends a JSON-RPC request and waits for its response.
// It encodes the provided [*Request], sends it to the server, waits for the
// server's response, decodes it, and returns the resulting [*Response].
//
// Example:
//
//	client := jsonrpc2.NewClientIO(conn) // Assume conn is an established io.ReadWriter
//	defer client.Close()
//	req := jsonrpc2.NewRequest(1, "arith.add", []int{2, 3})
//	resp, err := client.Call(context.Background(), req)
//	if err != nil {
//	    log.Fatalf("Call failed: %v", err)
//	}
//	if resp.IsError() {
//	    log.Fatalf("RPC Error: %v", resp.Error)
//	}
//	var result int
//	if err := resp.Result.Unmarshal(&result); err != nil {
//	    log.Fatalf("Failed to unmarshal result: %v", err)
//	}
//	fmt.Println("Result:", result) // Output: Result: 5
func (c *Client) Call(ctx context.Context, r *Request) (*Response, error) {
	rawResp, err := c.call(ctx, r, false) // false indicates it's not a notification

	if err != nil {
		return nil, err
	}

	var resp Response

	if err := c.d.Unmarshal(rawResp, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

// CallBatch sends a batch of requests ([Batch[*Request]]) and waits for the batch response.
// The server should respond with a JSON array containing [Response] objects, potentially
// out of order and possibly omitting responses for notifications included in the batch.
//
// If the server incorrectly responds with a single JSON object instead of an array,
// this method attempts to parse that single object as a [Response] and returns it
// wrapped in a [Batch] of length 1.
//
// Example:
//
//	req1 := jsonrpc2.NewRequest(1, "method1", nil)
//	req2 := jsonrpc2.NewRequest(2, "method2", nil)
//	batchReq := jsonrpc2.NewBatch[*jsonrpc2.Request](0)
//	batchReq.Add(req1, req2)
//
//	batchResp, err := client.CallBatch(context.Background(), batchReq)
//	if err != nil {
//	    log.Fatalf("CallBatch failed: %v", err)
//	}
//	// Process responses in batchResp, potentially using BatchCorrelate
//	jsonrpc2.BatchCorrelate(batchReq, batchResp, func(req *jsonrpc2.Request, res *jsonrpc2.Response) bool {
//	    // ... handle matched req/res pairs ...
//	    return true
//	})
func (c *Client) CallBatch(ctx context.Context, r Batch[*Request]) (Batch[*Response], error) {
	rawResp, err := c.call(ctx, r, false) // false indicates it's not a notification batch

	if err != nil {
		return nil, err
	}

	resp := NewBatch[*Response](len(r))

	switch jsonHintType(rawResp) {
	case TypeObject:
		var sresp *Response
		if err := c.d.Unmarshal(rawResp, &sresp); err != nil {
			return nil, err
		}

		resp.Add(sresp)
	default:
		// Array or bust
		if err := c.d.Unmarshal(rawResp, &resp); err != nil {
			return nil, err
		}
	}

	return resp, nil
}

// CallRaw sends a pre-encoded JSON-RPC request ([RawRequest]) and waits for its response.
// This is useful when the request payload is already available as raw JSON bytes.
// It decodes the response into a [*Response] struct.
//
// Example:
//
//	rawJSON := `{"jsonrpc":"2.0","method":"echo","params":["hello"],"id":10}`
//	rawReq := jsonrpc2.RawRequest(rawJSON)
//	resp, err := client.CallRaw(context.Background(), rawReq)
//	// ... process response as in Call example ...
func (c *Client) CallRaw(ctx context.Context, r RawRequest) (*Response, error) {
	// Note: We pass json.RawMessage(r) to call because RawRequest is just an alias.
	rawResp, err := c.call(ctx, json.RawMessage(r), false) // false indicates it's not a notification

	if err != nil {
		return nil, err
	}

	var resp Response

	if err := c.d.Unmarshal(rawResp, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

// CallWithTimeout is a convenience method that calls [Client.Call] with a derived context
// that includes the specified `timeout`.
// See [Client.Call] for more details.
func (c *Client) CallWithTimeout(ctx context.Context, timeout time.Duration, r *Request) (*Response, error) {
	tctx, stop := context.WithTimeout(ctx, timeout)
	defer stop()

	return c.Call(tctx, r)
}

// CallBatchWithTimeout is a convenience method that calls [Client.CallBatch] with a derived context
// that includes the specified `timeout`.
// See [Client.CallBatch] for more details.
func (c *Client) CallBatchWithTimeout(ctx context.Context, timeout time.Duration, r Batch[*Request]) (Batch[*Response], error) {
	tctx, stop := context.WithTimeout(ctx, timeout)
	defer stop()

	return c.CallBatch(tctx, r)
}

// CallRawWithTimeout is a convenience method that calls [Client.CallRaw] with a derived context
// that includes the specified `timeout`.
// See [Client.CallRaw] for more details.
func (c *Client) CallRawWithTimeout(ctx context.Context, timeout time.Duration, r RawRequest) (*Response, error) {
	tctx, stop := context.WithTimeout(ctx, timeout)
	defer stop()

	return c.CallRaw(tctx, r)
}

// Notify sends a JSON-RPC notification.
// It encodes the provided [*Notification], sends it to the server, and returns immediately
// without waiting for any response from the server.
//
// Example:
//
//	notif := jsonrpc2.NewNotification("logEvent", map[string]string{"level": "info", "message": "User logged in"})
//	err := client.Notify(context.Background(), notif)
//	if err != nil {
//	    log.Printf("Notify failed: %v", err) // Log error, but don't expect a response error
//	}
func (c *Client) Notify(ctx context.Context, n *Notification) error {
	if _, err := c.call(ctx, n, true); err != nil {
		return err
	}

	return nil
}

// NotifyBatch sends a batch of notifications ([Batch[*Notification]]).
// It encodes the batch, sends it to the server, and returns immediately.
// No response is expected or processed.
//
// Example:
//
//	notif1 := jsonrpc2.NewNotification("event1")
//	notif2 := jsonrpc2.NewNotification("event2")
//	batchNotif := jsonrpc2.NewBatch[*jsonrpc2.Notification](0)
//	batchNotif.Add(notif1, notif2)
//	err := client.NotifyBatch(context.Background(), batchNotif)
//	// ... handle potential send error ...
func (c *Client) NotifyBatch(ctx context.Context, n Batch[*Notification]) error {
	if _, err := c.call(ctx, n, true); err != nil {
		return err
	}

	return nil
}

// NotifyRaw sends a pre-encoded JSON-RPC notification ([RawNotification]).
// It sends the raw JSON bytes to the server and returns immediately.
//
// Example:
//
//	rawJSON := `{"jsonrpc":"2.0","method":"systemUpdate"}`
//	rawNotif := jsonrpc2.RawNotification(rawJSON)
//	err := client.NotifyRaw(context.Background(), rawNotif)
//	// ... handle potential send error ...
func (c *Client) NotifyRaw(ctx context.Context, n RawNotification) error {
	if _, err := c.call(ctx, json.RawMessage(n), true); err != nil {
		return err
	}

	return nil
}

// NotifyWithTimeout is a convenience method that calls [Client.Notify] with a derived context
// that includes the specified `timeout`.
// See [Client.Notify] for more details.
func (c *Client) NotifyWithTimeout(ctx context.Context, timeout time.Duration, n *Notification) error {
	tctx, stop := context.WithTimeout(ctx, timeout)
	defer stop()

	return c.Notify(tctx, n)
}

// NotifyBatchWithTimeout is a convenience method that calls [Client.NotifyBatch] with a derived context
// that includes the specified `timeout`.
// See [Client.NotifyBatch] for more details.
func (c *Client) NotifyBatchWithTimeout(ctx context.Context, timeout time.Duration, n Batch[*Notification]) error {
	tctx, stop := context.WithTimeout(ctx, timeout)
	defer stop()

	return c.NotifyBatch(tctx, n)
}

// NotifyRawWithTimeout is a convenience method that calls [Client.NotifyRaw] with a derived context
// that includes the specified `timeout`.
// See [Client.NotifyRaw] for more details.
func (c *Client) NotifyRawWithTimeout(ctx context.Context, timeout time.Duration, n RawNotification) error {
	tctx, stop := context.WithTimeout(ctx, timeout)
	defer stop()

	return c.NotifyRaw(tctx, n)
}
