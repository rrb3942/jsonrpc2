package jsonrpc2

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"
)

var (
	ErrDecoding     = errors.New("jsonrpc2: decoding error")
	ErrJSONTooLarge = errors.New("JSON payload larger than configured read limit")
)

// Decoder represents a compatible decoder for use with [StreamServer] and [Client].
//
// A compatible implementation must support all types and interfaces of [encoding/json] including,
// but not limited to, [json.Marshaler], [json.Unmarshaler], [json.RawMessage], [json.Number] and the `omitzero` struct
// tag introduced in go 1.24.
//
// It is recommended that any implementation also support [io.Closer] and [DeadlineReader] if at all possible.
type Decoder interface {
	Decode(context.Context, any) error
	// Unmarshal must be go-routine safe
	Unmarshal([]byte, any) error
}

// NewDecoderFunc defines a function returning a new [Decoder].
type NewDecoderFunc func(io.Reader) Decoder

// StreamDecoder is a configurable [Decoder] supporting limited read sizes and idle timeouts.
type StreamDecoder struct {
	r  io.Reader
	lr *io.LimitedReader
	d  *json.Decoder
	n  int64
	t  time.Duration
}

// NewDecoder returns a new [Decoder] utilizing r as the source.
// It is the default [NewDecoderFunc] used by [Server].
//
//nolint:ireturn //Implements NewDecoderFunc
func NewDecoder(r io.Reader) Decoder {
	return NewStreamDecoder(r)
}

// NewStreamDecoder returns a new [*StreamDecoder] utilizing r as the source.
func NewStreamDecoder(r io.Reader) *StreamDecoder {
	return &StreamDecoder{r: r, d: json.NewDecoder(r)}
}

// SetLimit sets the maximum bytes to decode in a single call. The decoder will return [ErrJSONTooLarge] if the limit is reached on a call to Decode.
func (i *StreamDecoder) SetLimit(n int64) {
	i.n = n

	i.lr = &io.LimitedReader{R: i.r, N: i.n}

	i.d = json.NewDecoder(i.lr)
}

// SetIdleTimeout sets an idle timeout for decoding.
//
// If the underlying [io.Reader] supports [io.Closer], the reader will be closed if the timeout is reached.
//
// If the underlying [io.Reader] supports [DeadlineReader] it will be used for implementing the timeout instead, without closing the reader.
//
// If neither method is supported, timeouts are not implemented.
func (i *StreamDecoder) SetIdleTimeout(d time.Duration) {
	i.t = d
}

// Used for checking for if we hit read limits and changing EOF to ErrJSONTooLarge.
func (i *StreamDecoder) ioErr(e error) error {
	if i.lr != nil {
		if errors.Is(e, io.EOF) {
			if i.lr.N <= 0 {
				return ErrJSONTooLarge
			}
		}
	}

	return e
}

func (i *StreamDecoder) cancelDecode(ctx context.Context, cReader io.Closer, v any) error {
	var dctx context.Context

	var stop context.CancelFunc

	deadLiner, haveDeadline := cReader.(DeadlineReader)

	if haveDeadline {
		// Reset timeout if it had fired
		if err := deadLiner.SetReadDeadline(time.Time{}); err != nil {
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
			_ = deadLiner.SetReadDeadline(time.Now())
			return
		}

		cReader.Close()
	})

	dErr := i.ioErr(i.d.Decode(v))

	if !after() {
		wg.Wait()
		return errors.Join(dErr, dctx.Err())
	}

	return dErr
}

// Decode decodes the next json value from the underlying reader into v.
func (i *StreamDecoder) Decode(ctx context.Context, v any) error {
	// Reset read limit if configured
	if i.lr != nil {
		i.lr.N = i.n
	}

	// Supports close, use that for cancel
	if c, ok := i.r.(io.Closer); ok {
		return i.cancelDecode(ctx, c, v)
	}

	// Does not support deadline or close, no way to cancel
	return i.ioErr(i.d.Decode(v))
}

// Unmarshal unmarshals data into v.
func (i *StreamDecoder) Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// Close will close the underlying reader if it supports [io.Closer].
func (i *StreamDecoder) Close() error {
	if c, ok := i.r.(io.Closer); ok {
		return c.Close()
	}

	return nil
}
