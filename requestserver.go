package jsonrpc2

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
)

// RequestServer represents a single jsonrpc2 server.
//
// All requests handled by the RequestServer will have the context key [CtxRequestServer] so to the current [*RequestServer].
type RequestServer struct {
	Callbacks Callbacks
	decoder   PacketDecoder
	Handler   Handler
	encoder   PacketEncoder
	// Run batches in serial without go-routine fan out
	SerialBatch bool
	// Don't run requests in a separate go-routine
	NoRoutines bool
	// Wait for any pending requests instead of immediately signaling for a cancel
	// when the Run() is about to return
	WaitOnClose bool
}

func newRequestServer(d, e any, handler Handler) *RequestServer {
	var dec PacketDecoder

	var enc PacketEncoder

	switch v := d.(type) {
	case Decoder:
		dec = &decoderShim{v}
	case PacketDecoder:
		dec = v
	}

	switch v := e.(type) {
	case Encoder:
		enc = &encoderShim{v}
	case PacketEncoder:
		enc = v
	}

	rp := &RequestServer{decoder: dec, encoder: enc, Handler: handler}
	rp.Callbacks.OnHandlerPanic = DefaultOnHandlerPanic

	return rp
}

// NewStreamServer returns a new [*RequestServer] with a [Handler] of handle.
//
// It operates over streaming streaming connections.
func NewStreamServer(d Decoder, e Encoder, handler Handler) *RequestServer {
	return newRequestServer(d, e, handler)
}

// NewStreamServerFromIO returns a new [*RequestServer] with a [Handler] of handle.
//
// rw will be wrapped with default encoders and decoders as returned by [NewDecoder] and [NewEncoder].
func NewStreamServerFromIO(rw io.ReadWriter, handler Handler) *RequestServer {
	return NewStreamServer(NewDecoder(rw), NewEncoder(rw), handler)
}

// NewPacketServer returns a new [*RequestServer] with a [Handler] of handle.
//
// It operates over connectionless packet sockets.
func NewPacketServer(d PacketDecoder, e PacketEncoder, handler Handler) *RequestServer {
	return newRequestServer(d, e, handler)
}

// NewRequestServer returns a new [*RequestServer] with a [Handler] of handle.
//
// rw will be wrapped with default encoders and decoders as returned by [NewPacketDecoder] and [NewPacketEncoder].
func NewRequestServerFromPacket(rw net.PacketConn, handler Handler) *RequestServer {
	return NewPacketServer(NewPacketDecoder(rw), NewPacketEncoder(rw), handler)
}

func (rp *RequestServer) runRequest(ctx context.Context, r json.RawMessage, from net.Addr) {
	if resp := handleRequest(ctx, rp.Handler, rp.decoder, &rp.Callbacks, r); resp != nil {
		if err := rp.encoder.EncodeTo(ctx, resp, from); err != nil {
			rp.Callbacks.runOnEncodingError(ctx, resp, err)
		}
	}
}

func (rp *RequestServer) runRequests(ctx context.Context, raw json.RawMessage, from net.Addr) {
	var objs []json.RawMessage

	var resps []any

	// Split into individual objects
	err := rp.decoder.Unmarshal(raw, &objs)

	if err != nil {
		rp.Callbacks.runOnDecodingError(ctx, raw, err)

		_ = rp.encoder.EncodeTo(ctx, &Response{ID: NewNullID(), Error: ErrInvalidRequest.WithData(err.Error())}, from)

		return
	}

	// Handle empty array case
	if len(objs) == 0 {
		resp := &Response{ID: NewNullID(), Error: ErrInvalidRequest}
		if err := rp.encoder.EncodeTo(ctx, resp, from); err != nil {
			rp.Callbacks.runOnEncodingError(ctx, resp, err)
		}

		return
	}

	resps = make([]any, 0, len(objs))

	if len(objs) == 1 || rp.SerialBatch {
		for _, jReq := range objs {
			resp := handleRequest(ctx, rp.Handler, rp.decoder, &rp.Callbacks, jReq)

			if resp != nil {
				resps = append(resps, resp)
			}
		}
	} else {
		var wg sync.WaitGroup

		results := make(chan any, len(objs))

		for _, jReq := range objs {
			wg.Add(1)

			go func(gctx context.Context, ch chan<- any, jr json.RawMessage) {
				defer wg.Done()
				ch <- handleRequest(gctx, rp.Handler, rp.decoder, &rp.Callbacks, jr)
			}(ctx, results, jReq)
		}

		wg.Wait()
		close(results)

		for resp := range results {
			if resp != nil {
				resps = append(resps, resp)
			}
		}
	}

	if len(resps) > 0 {
		if err := rp.encoder.EncodeTo(ctx, resps, from); err != nil {
			rp.Callbacks.runOnEncodingError(ctx, resps, err)
		}
	}
}

func (rp *RequestServer) run(ctx context.Context, buf json.RawMessage, from net.Addr) {
	ctx = context.WithValue(ctx, CtxFromAddr, from)

	switch buf[0] {
	case '[':
		rp.runRequests(ctx, buf, from)
	case '{':
		rp.runRequest(ctx, buf, from)
	default:
		_ = rp.encoder.EncodeTo(ctx, &Response{ID: NewNullID(), Error: ErrParse}, from)
	}
}

func (rp *RequestServer) Close() error {
	var err error

	if dc, ok := rp.decoder.(io.Closer); ok {
		err = dc.Close()
	}

	if ec, ok := rp.encoder.(io.Closer); ok {
		return errors.Join(err, ec.Close())
	}

	return err
}

// Run runs the server until ctx is cancelled or the connection is broken.
func (rp *RequestServer) Run(ctx context.Context) (err error) {
	var wg sync.WaitGroup

	sctx, stop := context.WithCancel(ctx)

	defer func() {
		if rp.WaitOnClose {
			wg.Wait()
			stop()
		} else {
			stop()
			wg.Wait()
		}

		err = errors.Join(err, ctx.Err(), rp.Close())

		rp.Callbacks.runOnExit(ctx, err)
	}()

	for {
		var from net.Addr

		var buf json.RawMessage

		from, err = rp.decoder.DecodeFrom(sctx, &buf)

		if err != nil || ctx.Err() != nil {
			return
		}

		if len(buf) == 0 {
			continue
		}

		if rp.NoRoutines {
			rp.run(ctx, buf, from)
		} else {
			wg.Add(1)

			go func() {
				defer wg.Done()
				rp.run(sctx, buf, from)
			}()
		}
	}
}
