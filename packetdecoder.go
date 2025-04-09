package jsonrpc2

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"
)

const DefaultPacketBufferSize = 65507

// PacketDecoder represents a decoder for packet connections.
//
// PacketDecoder should only decode a single object or array per-datagram.
//
// PacketDecoder must be go-routine safe.
type PacketDecoder interface {
	DecodeFrom(context.Context, any) (net.Addr, error)
	Unmarshal([]byte, any) error
}

// NewPacketDecoderFunc defines a function returning a new [PacketDecoder].
type NewPacketDecoderFunc func(r net.PacketConn) PacketDecoder

// PacketConnDecoder is a configurable [PacketDecoder] supporting limited read sizes and idle timeouts.
type PacketConnDecoder struct {
	r net.PacketConn
	n int64
	t time.Duration
}

// NewPacketDecoder returns a new [PacketDecoder] utilizing r as the source.
// It is the default [NewPacketDecoderFunc] used by [Server].
//
//nolint:ireturn //Implements NewPacketDecoderFunc
func NewPacketDecoder(r net.PacketConn) PacketDecoder {
	return NewPacketConnDecoder(r)
}

// NewPacketConnDecoder returns a new [*PacketConnDecoder] utilizing r as the source.
func NewPacketConnDecoder(r net.PacketConn) *PacketConnDecoder {
	return &PacketConnDecoder{r: r}
}

// SetLimit sets the maximum allowed datagram size.
func (i *PacketConnDecoder) SetLimit(n int64) {
	i.n = n
}

// SetIdleTimeout sets an idle timeout for decoding.
//
// If the underlying [io.Reader] supports [io.Closer], the reader will be closed if the timeout is reached.
//
// If the underlying [io.Reader] supports [DeadlineReader] it will be used for implementing the timeout instead, without closing the reader.
//
// If neither method is supported, timeouts are not implemented.
func (i *PacketConnDecoder) SetIdleTimeout(d time.Duration) {
	i.t = d
}

// DecodeFrom implements [PacketDecoder].
func (i *PacketConnDecoder) DecodeFrom(ctx context.Context, v any) (net.Addr, error) {
	dctx, stop := context.WithCancel(ctx)
	defer stop()

	// Reset any deadline for a fresh read
	timeout := time.Time{}

	// Apply idle timeout
	if i.t > 0 {
		timeout = time.Now().Add(i.t)
	}

	if err := i.r.SetReadDeadline(timeout); err != nil {
		return nil, err
	}

	after := context.AfterFunc(dctx, func() {
		_ = i.r.SetReadDeadline(time.Now())
	})

	readSize := i.n

	if i.n <= 0 {
		readSize = DefaultPacketBufferSize
	}

	buf := make([]byte, readSize)

	read, addr, err := i.r.ReadFrom(buf)

	if !after() {
		return addr, errors.Join(err, dctx.Err())
	}

	if err != nil {
		return addr, err
	}

	return addr, i.Unmarshal(buf[:read], v)
}

// Unmarshal unmarshals data into v.
func (i *PacketConnDecoder) Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// Close will close the underlying reader if it supports [io.Closer].
func (i *PacketConnDecoder) Close() error {
	return i.r.Close()
}
