package jsonrpc2

import (
	"context"
	"encoding/json"
)

// Callbacks contains functions to run at on various events in a [*StreamServer].
//
// Callbacks must not modify the [*StreamServer] in any way  when called. It is only provided for inspection.
type Callbacks struct {
	// Run when the server returns from Run. Returns any error associated with the close (io.EOF, context.Canceled, etc)
	OnExit func(context.Context, error)
	// Run whenever a json message cannot be parsed into a Request
	OnDecodingError func(context.Context, json.RawMessage, error)
	// Run whenever writing to the encoder fails. The error can be inspected to determine if you wish to cancel the server.
	OnEncodingError func(context.Context, any, error)
	// Run whenever a json message cannot be parsed into a Request
	OnHandlerPanic func(context.Context, *Request, any)
}

func (c *Callbacks) runOnExit(ctx context.Context, e error) {
	if c.OnExit != nil {
		c.OnExit(ctx, e)
	}
}

func (c *Callbacks) runOnDecodingError(ctx context.Context, m json.RawMessage, e error) {
	if c.OnDecodingError != nil {
		c.OnDecodingError(ctx, m, e)
	}
}

func (c *Callbacks) runOnEncodingError(ctx context.Context, d any, e error) {
	if c.OnEncodingError != nil {
		c.OnEncodingError(ctx, d, e)
	}
}

func (c *Callbacks) runOnHandlerPanic(ctx context.Context, r *Request, recovery any) {
	if c.OnHandlerPanic != nil {
		c.OnHandlerPanic(ctx, r, recovery)
	}
}
