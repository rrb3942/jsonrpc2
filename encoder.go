package jsonrpc2

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

var ErrEncoding = errors.New("jsonrpc2: encoding error")

// Encoder represents a compatible Encoder for use with [StreamServer] and [Client].
//
// A compatible implementation MUST be go-routine safe and support all types and interfaces of [encoding/json] including,
// but not limited to, [json.Marshaler], [json.Unmarshaler], [json.RawMessage], [json.Number] and the `omitzero` struct
// tag introduced in go 1.24.
//
// It is recommended that any implementation also support [io.Closer] and [DeadlineWriter] if at all possible.
type Encoder interface {
	Encode(context.Context, any) error
}

// NewEncoderFunc defines a function returning a new [Encoder].
type NewEncoderFunc func(io.Writer) Encoder

type lockWriter struct {
	w  io.Writer
	mu sync.Mutex
}

func (lw *lockWriter) Write(data []byte) (int, error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()

	return lw.w.Write(data)
}

// StreamEncoder is a configurable [Encoder] supporting idle timeouts.
type StreamEncoder struct {
	lw *lockWriter
	w  io.Writer
	e  JSONEncoder
	t  time.Duration
}

// NewEncoder returns a new [Encoder] utilizing w as the output.
// It is the default [NewEncoderFunc] used by [Server].
//
//nolint:ireturn //Implements NewEncoderFunc
func NewEncoder(r io.Writer) Encoder {
	return NewStreamEncoder(r)
}

// NewStreamEncoder returns a new [*StreamEncoder] utilizing w as the output.
func NewStreamEncoder(w io.Writer) *StreamEncoder {
	lw := &lockWriter{w: w}
	return &StreamEncoder{lw: lw, w: w, e: NewJSONEncoder(lw)}
}

// SetIdleTimeout sets an idle timeout for Encoding.
//
// If the underlying [io.Writer] supports [io.Closer], the Writer will be closed if the timeout is reached.
//
// If the underlying [io.Writer] supports [DeadlineWriter] it will be used for implementing the timeout instead, without closing the Writer.
//
// If neither method is supported, timeouts are not implemented.
func (i *StreamEncoder) SetIdleTimeout(d time.Duration) {
	i.t = d
}

func (i *StreamEncoder) cancelEncode(ctx context.Context, cWriter io.Closer, v any) error {
	var dctx context.Context

	var stop context.CancelFunc

	deadLiner, haveDeadline := cWriter.(DeadlineWriter)

	if haveDeadline {
		// Reset timeout if it had fired
		if err := deadLiner.SetWriteDeadline(time.Time{}); err != nil {
			return err
		}
	}

	if i.t > 0 {
		// With idle timeout
		dctx, stop = context.WithTimeout(ctx, i.t)
	} else {
		dctx, stop = context.WithCancel(ctx)
	}

	defer stop()

	var wg sync.WaitGroup

	wg.Add(1)

	after := context.AfterFunc(dctx, func() {
		defer wg.Done()

		if haveDeadline {
			_ = deadLiner.SetWriteDeadline(time.Now())
			return
		}

		cWriter.Close()
	})

	err := i.e.Encode(v)

	if !after() {
		return errors.Join(err, dctx.Err())
	}

	return err
}

// Encode encodes the next json value from the underlying Writer into v.
func (i *StreamEncoder) Encode(ctx context.Context, v any) error {
	// Supports close, use that for cancel
	if c, ok := i.w.(io.Closer); ok {
		return i.cancelEncode(ctx, c, v)
	}

	// Does not support deadline or close, no way to cancel
	return i.e.Encode(v)
}

// Close will close the underlying writer if it supports [io.Closer].
func (i *StreamEncoder) Close() error {
	if c, ok := i.w.(io.Closer); ok {
		return c.Close()
	}

	return nil
}
