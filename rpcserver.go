package jsonrpc2

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
)

var (
	// Used for checking for json errors
	syntaxErr   = &json.SyntaxError{}
	jsonTypeErr = &json.UnmarshalTypeError{}
)

// RPCServer represents a single jsonrpc2 server that handles rpc requests and notifications.
//
// All requests handled by the RPCServer will have the context key [CtxRPCServer] so to the current [*RPCServer].
type RPCServer struct {
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

func newRPCServer(d, e any, handler Handler) *RPCServer {
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

	rp := &RPCServer{decoder: dec, encoder: enc, Handler: handler}
	rp.Callbacks.OnHandlerPanic = DefaultOnHandlerPanic

	return rp
}

// NewStreamServer returns a new [*RPCServer] with a [Handler] of handle.
//
// It operates over streaming streaming connections.
func NewStreamServer(d Decoder, e Encoder, handler Handler) *RPCServer {
	return newRPCServer(d, e, handler)
}

// NewStreamServerFromIO returns a new [*RPCServer] with a [Handler] of handle.
//
// rw will be wrapped with default encoders and decoders as returned by [NewDecoder] and [NewEncoder].
func NewStreamServerFromIO(rw io.ReadWriter, handler Handler) *RPCServer {
	return NewStreamServer(NewDecoder(rw), NewEncoder(rw), handler)
}

// NewPacketServer returns a new [*RPCServer] with a [Handler] of handle.
//
// It operates over connectionless packet sockets.
func NewPacketServer(d PacketDecoder, e PacketEncoder, handler Handler) *RPCServer {
	return newRPCServer(d, e, handler)
}

// NewRPCServer returns a new [*RPCServer] with a [Handler] of handle.
//
// rw will be wrapped with default encoders and decoders as returned by [NewPacketDecoder] and [NewPacketEncoder].
func NewRPCServerFromPacket(rw net.PacketConn, handler Handler) *RPCServer {
	return NewPacketServer(NewPacketDecoder(rw), NewPacketEncoder(rw), handler)
}

func (rp *RPCServer) handleRequest(ctx context.Context, rpc json.RawMessage) (res any) {
	var req Request

	err := rp.decoder.Unmarshal(rpc, &req)

	if err != nil {
		rp.Callbacks.runOnDecodingError(ctx, rpc, err)

		return &Response{ID: NewNullID(), Error: ErrInvalidRequest.WithData(err.Error())}
	}

	// Catch panics from inside the handler
	defer func() {
		if r := recover(); r != nil {
			res = ErrInternalError

			rp.Callbacks.runOnHandlerPanic(ctx, &req, r)
		}
	}()

	result, err := rp.Handler.Handle(ctx, &req)

	if req.IsNotification() {
		return nil
	}

	if err != nil {
		return req.ResponseWithError(err)
	}

	// Special return types
	switch r := result.(type) {
	case *Response:
		return r
	case RawResponse:
		return json.RawMessage(r)
	}

	if result == nil {
		result = nullValue
	}

	return req.ResponseWithResult(result)
}

func (rp *RPCServer) runRequest(ctx context.Context, r json.RawMessage, from net.Addr) {
	if resp := rp.handleRequest(ctx, r); resp != nil {
		if err := rp.encoder.EncodeTo(ctx, resp, from); err != nil {
			rp.Callbacks.runOnEncodingError(ctx, resp, err)
		}
	}
}

func (rp *RPCServer) runRequests(ctx context.Context, raw json.RawMessage, from net.Addr) {
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
			resp := rp.handleRequest(ctx, jReq)

			if resp != nil {
				resps = append(resps, resp)
			}
		}
	} else {
		var wg sync.WaitGroup

		var respMu sync.Mutex

		for _, jReq := range objs {
			wg.Add(1)

			go func(gctx context.Context, jr json.RawMessage) {
				defer wg.Done()

				res := rp.handleRequest(gctx, jr)

				respMu.Lock()
				defer respMu.Unlock()

				resps = append(resps, res)
			}(ctx, jReq)
		}

		wg.Wait()
	}

	if len(resps) > 0 {
		if err := rp.encoder.EncodeTo(ctx, resps, from); err != nil {
			rp.Callbacks.runOnEncodingError(ctx, resps, err)
		}
	}
}

func (rp *RPCServer) run(ctx context.Context, buf json.RawMessage, from net.Addr) {
	ctx = context.WithValue(ctx, CtxFromAddr, from)

	switch jsonHintType(buf) {
	case TypeArray:
		rp.runRequests(ctx, buf, from)
	case TypeObject:
		rp.runRequest(ctx, buf, from)
	default:
		_ = rp.encoder.EncodeTo(ctx, &Response{ID: NewNullID(), Error: ErrParse}, from)
	}
}

func (rp *RPCServer) Close() error {
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
func (rp *RPCServer) Run(ctx context.Context) (err error) {
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

		if err != nil {
			if errors.As(err, &syntaxErr) {
				_ = rp.encoder.EncodeTo(ctx, NewResponseError(ErrParse.WithData(err)), from)
				err = nil
				continue
			}

			if errors.As(err, &jsonTypeErr) {
				_ = rp.encoder.EncodeTo(ctx, NewResponseError(ErrInvalidRequest.WithData(err)), from)
				err = nil
				continue
			}

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
