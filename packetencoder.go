package jsonrpc2

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"time"
)

// PacketEncoder represents a encoder for packet connections.
//
// PacketEncoder should only encode a single object or array per-datagram.
//
// PacketEncoder must be go-routine safe.
type PacketEncoder interface {
	EncodeTo(context.Context, any, net.Addr) error
	Marshal(any) ([]byte, error)
}

// NewPacketEncoderFunc defines a function returning a new [PacketEncoder].
type NewPacketEncoderFunc func(r net.PacketConn) PacketEncoder

// PacketConnEncoder is a configurable [PacketEncoder] supporting idle timeouts.
type PacketConnEncoder struct {
	w net.PacketConn
	t time.Duration
}

// NewPacketEncoder returns a new [PacketEncoder] utilizing w as the output.
// It is the default [NewPacketEncoderFunc] used by [Server].
//
//nolint:ireturn //Implements NewPacketEncoderFunc
func NewPacketEncoder(w net.PacketConn) PacketEncoder {
	return NewPacketConnEncoder(w)
}

// NewPacketConnEncoder returns a new [*PacketConnEncoder] utilizing r as the source.
func NewPacketConnEncoder(w net.PacketConn) *PacketConnEncoder {
	return &PacketConnEncoder{w: w}
}

// SetIdleTimeout sets an idle timeout for decoding.
//
// If the underlying [io.Reader] supports [io.Closer], the reader will be closed if the timeout is reached.
//
// If the underlying [io.Reader] supports [DeadlineReader] it will be used for implementing the timeout instead, without closing the reader.
//
// If neither method is supported, timeouts are not implemented.
func (i *PacketConnEncoder) SetIdleTimeout(d time.Duration) {
	i.t = d
}

// DecodeFrom implements [PacketEncoder].
func (i *PacketConnEncoder) EncodeTo(ctx context.Context, v any, addr net.Addr) error {
	var dctx context.Context

	var stop context.CancelFunc

	if i.t > 0 {
		// With idle timeout
		dctx, stop = context.WithTimeout(ctx, i.t)
	} else {
		dctx, stop = context.WithCancel(ctx)
	}

	defer stop()

	buf, err := i.Marshal(v)

	if err != nil {
		return err
	}

	// Reset deadline if it had fired
	if terr := i.w.SetWriteDeadline(time.Time{}); terr != nil {
		return terr
	}

	var wg sync.WaitGroup

	wg.Add(1)

	after := context.AfterFunc(dctx, func() {
		defer wg.Done()

		_ = i.w.SetWriteDeadline(time.Now())
	})

	_, err = i.w.WriteTo(buf, addr)

	if !after() {
		wg.Wait()
		return errors.Join(err, dctx.Err())
	}

	return err
}

// Unmarshal unmarshals data into v.
func (i *PacketConnEncoder) Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

// Close will close the underlying reader if it supports [io.Closer].
func (i *PacketConnEncoder) Close() error {
	return i.w.Close()
}
