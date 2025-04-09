package jsonrpc2

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
)

// PacketServer represents a single jsonrpc2 server on a packet connection.
//
// All requests handled by the PacketServer will have the context key [CtxPacketServer] so to the current [*PacketServer].
type PacketServer struct {
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

// NewPacketServer returns a new [*PacketServer] with a [Handler] of handle.
// The server will read requests from the decoder and send any responses to the encoder.
func NewPacketServer(d PacketDecoder, e PacketEncoder, handler Handler) *PacketServer {
	connHandler := &PacketServer{
		decoder: d,
		encoder: e,
		Handler: handler,
	}

	return connHandler
}

// NewPacketServer returns a new [*PacketServer] with a [Handler] of handle.
//
// rw will be wrapped with default encoders and decoders as returned by [NewPacketDecoder] and [NewPacketEncoder].
func NewPacketServerFromIO(rw net.PacketConn, handler Handler) *PacketServer {
	connHandler := &PacketServer{
		decoder: NewPacketDecoder(rw),
		encoder: NewPacketEncoder(rw),
		Handler: handler,
	}

	return connHandler
}

func (p *PacketServer) runRequest(ctx context.Context, r json.RawMessage, from net.Addr) {
	if resp := handleRequest(ctx, p.Handler, p.decoder, &p.Callbacks, r); resp != nil {
		if err := p.encoder.EncodeTo(ctx, resp, from); err != nil {
			p.Callbacks.runOnEncodingError(ctx, resp, err)
		}
	}
}

func (p *PacketServer) runRequests(ctx context.Context, raw json.RawMessage, from net.Addr) {
	var objs []json.RawMessage

	var resps []any

	// Split into individual objects
	err := p.decoder.Unmarshal(raw, &objs)

	if err != nil {
		p.Callbacks.runOnDecodingError(ctx, raw, err)

		_ = p.encoder.EncodeTo(ctx, &Response{ID: NewNullID(), Error: ErrInvalidRequest.WithData(err.Error())}, from)

		return
	}

	resps = make([]any, 0, len(objs))

	if len(objs) == 1 || p.SerialBatch {
		for _, jReq := range objs {
			resp := handleRequest(ctx, p.Handler, p.decoder, &p.Callbacks, jReq)

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
				ch <- handleRequest(gctx, p.Handler, p.decoder, &p.Callbacks, jr)
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
		if err := p.encoder.EncodeTo(ctx, resps, from); err != nil {
			p.Callbacks.runOnEncodingError(ctx, resps, err)
		}
	}
}

func (p *PacketServer) run(ctx context.Context, buf json.RawMessage, from net.Addr) {
	ctx = context.WithValue(ctx, CtxFromAddr, from)

	switch buf[0] {
	case '[':
		p.runRequests(ctx, buf, from)
	case '{':
		p.runRequest(ctx, buf, from)
	default:
		_ = p.encoder.EncodeTo(ctx, &Response{ID: NewNullID(), Error: ErrParse}, from)
	}
}

func (p *PacketServer) Close() error {
	var err error

	if dc, ok := p.decoder.(io.Closer); ok {
		err = dc.Close()
	}

	if ec, ok := p.encoder.(io.Closer); ok {
		return errors.Join(err, ec.Close())
	}

	return err
}

// Run runs the server until ctx is cancelled or the connection is broken.
func (p *PacketServer) Run(ctx context.Context) error {
	defer p.Close()

	var wg sync.WaitGroup
	defer wg.Wait()

	sctx, stop := context.WithCancel(context.WithValue(ctx, CtxPacketServer, p))
	defer stop()

	var err error

	var from net.Addr

	for {
		var buf json.RawMessage

		from, err = p.decoder.DecodeFrom(sctx, &buf)

		if cErr := ctx.Err(); cErr != nil {
			err = errors.Join(err, cErr)
			break
		}

		if err != nil {
			break
		}

		if len(buf) == 0 {
			continue
		}

		if p.NoRoutines {
			p.run(ctx, buf, from)
		} else {
			wg.Add(1)

			go func() {
				defer wg.Done()
				p.run(sctx, buf, from)
			}()
		}
	}

	if p.WaitOnClose {
		wg.Wait()
	}

	p.Callbacks.runOnExit(ctx, err)

	return err
}
