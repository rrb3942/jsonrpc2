package jsonrpc2

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
)

// StreamServer represents a single jsonrpc2 server connection/stream.
//
// All requests handled by the StreamServer will have the context key [CtxStreamServer] so to the current [*StreamServer].
type StreamServer struct {
	Callbacks Callbacks
	decoder   Decoder
	Handler   Handler
	encoder   Encoder
	// Run batches in serial without go-routine fan out
	SerialBatch bool
	// Don't run requests in a separate go-routine
	NoRoutines bool
	// Wait for any pending requests instead of immediately signaling for a cancel
	// when the Run() is about to return
	WaitOnClose bool
}

// NewStreamServer returns a new [*StreamServer] with a [Handler] of handle.
// The server will read requests from the decoder and send any responses to the encoder.
func NewStreamServer(d Decoder, e Encoder, handler Handler) *StreamServer {
	connHandler := &StreamServer{
		decoder: d,
		encoder: e,
		Handler: handler,
	}

	connHandler.Callbacks.OnHandlerPanic = DefaultOnHandlerPanic

	return connHandler
}

// NewStreamServer returns a new [*StreamServer] with a [Handler] of handle.
//
// rw will be wrapped with default encoders and decoders as returned by [NewDecoder] and [NewEncoder].
func NewStreamServerFromIO(rw io.ReadWriter, handler Handler) *StreamServer {
	connHandler := &StreamServer{
		decoder: NewDecoder(rw),
		encoder: NewEncoder(rw),
		Handler: handler,
	}

	connHandler.Callbacks.OnHandlerPanic = DefaultOnHandlerPanic

	return connHandler
}

func (s *StreamServer) runRequest(ctx context.Context, r json.RawMessage) {
	if resp := handleRequest(ctx, s.Handler, s.decoder, &s.Callbacks, r); resp != nil {
		if err := s.encoder.Encode(ctx, resp); err != nil {
			s.Callbacks.runOnEncodingError(ctx, resp, err)
		}
	}
}

func (s *StreamServer) runRequests(ctx context.Context, raw json.RawMessage) {
	var objs []json.RawMessage

	var resps []any

	// Split into individual objects
	err := s.decoder.Unmarshal(raw, &objs)

	if err != nil {
		s.Callbacks.runOnDecodingError(ctx, raw, err)

		_ = s.encoder.Encode(ctx, &Response{ID: NewNullID(), Error: ErrInvalidRequest.WithData(err.Error())})

		return
	}

	// Handle empty array case
	if len(objs) == 0 {
		resp := &Response{ID: NewNullID(), Error: ErrInvalidRequest}
		if err := s.encoder.Encode(ctx, resp); err != nil {
			s.Callbacks.runOnEncodingError(ctx, resp, err)
		}

		return
	}

	resps = make([]any, 0, len(objs))

	if len(objs) == 1 || s.SerialBatch {
		for _, jReq := range objs {
			resp := handleRequest(ctx, s.Handler, s.decoder, &s.Callbacks, jReq)

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
				ch <- handleRequest(gctx, s.Handler, s.decoder, &s.Callbacks, jr)
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
		if err := s.encoder.Encode(ctx, resps); err != nil {
			s.Callbacks.runOnEncodingError(ctx, resps, err)
		}
	}
}

func (s *StreamServer) run(ctx context.Context, buf json.RawMessage) {
	switch buf[0] {
	case '[':
		s.runRequests(ctx, buf)
	case '{':
		s.runRequest(ctx, buf)
	default:
		_ = s.encoder.Encode(ctx, &Response{ID: NewNullID(), Error: ErrParse})
	}
}

func (s *StreamServer) Close() error {
	var err error

	if dc, ok := s.decoder.(io.Closer); ok {
		err = dc.Close()
	}

	if ec, ok := s.encoder.(io.Closer); ok {
		return errors.Join(err, ec.Close())
	}

	return err
}

// Run runs the server until ctx is cancelled or the stream is broken.
func (s *StreamServer) Run(ctx context.Context) error {
	defer s.Close()

	var wg sync.WaitGroup
	defer wg.Wait()

	sctx, stop := context.WithCancel(ctx)
	defer stop()

	var err error

	for {
		var buf json.RawMessage

		err = s.decoder.Decode(sctx, &buf)

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

		if s.NoRoutines {
			s.run(ctx, buf)
		} else {
			wg.Add(1)

			go func() {
				defer wg.Done()
				s.run(sctx, buf)
			}()
		}
	}

	if s.WaitOnClose {
		wg.Wait()
	}

	s.Callbacks.runOnExit(ctx, err)

	return err
}
