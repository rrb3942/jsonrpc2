package jsonrpc2

import (
	"context"
	"io"
	"net"
)

// decoderShim shims a [Decoder] to be used as a [PacketDecoder].
type decoderShim struct {
	Decoder
}

func (d *decoderShim) DecodeFrom(ctx context.Context, v any) (net.Addr, error) {
	return nil, d.Decode(ctx, v)
}

func (d *decoderShim) Close() error {
	if closer, ok := d.Decoder.(io.Closer); ok {
		return closer.Close()
	}

	return nil
}

// encoderShim shims an [Encoder] to be used as a [PacketEncoder].
type encoderShim struct {
	Encoder
}

func (e *encoderShim) EncodeTo(ctx context.Context, v any, _ net.Addr) error {
	return e.Encode(ctx, v)
}

func (e *encoderShim) Close() error {
	if closer, ok := e.Encoder.(io.Closer); ok {
		return closer.Close()
	}

	return nil
}
