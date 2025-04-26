package jsonrpc2

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"
)

// DefaultPacketBufferSize specifies the default read buffer size (65507 bytes)
// used by [PacketConnDecoder.DecodeFrom] if no explicit limit is set via
// [PacketConnDecoder.SetLimit]. This size is chosen to accommodate the
// theoretical maximum UDP datagram size, although practical limits may be lower.
const DefaultPacketBufferSize = 65507

// PacketDecoder defines the interface for decoding JSON-RPC messages from packet-based
// network connections like UDP ([net.PacketConn]). It's used by [Server] for
// handling datagram protocols.
//
// Implementations are expected to decode a single JSON object or array per datagram.
// All methods must be safe for concurrent use by multiple goroutines.
type PacketDecoder interface {
	// DecodeFrom reads a single packet (datagram) from the underlying connection,
	// decodes the JSON payload into the value pointed to by v, and returns the
	// network address ([net.Addr]) of the sender.
	// The context can be used for cancellation or deadlines.
	DecodeFrom(ctx context.Context, v any) (net.Addr, error)

	// Unmarshal parses the JSON-encoded data from a byte slice (typically a received packet)
	// and stores the result in the value pointed to by v. This method must be goroutine-safe.
	Unmarshal(data []byte, v any) error
}

// NewPacketDecoderFunc defines the signature for functions that create a new [PacketDecoder].
// This allows customizing the packet decoder used by [Server].
type NewPacketDecoderFunc func(conn net.PacketConn) PacketDecoder

// PacketConnDecoder provides a default implementation of [PacketDecoder] for use
// with [net.PacketConn]. It reads datagrams, decodes the JSON content, and supports
// configurable read limits and idle timeouts.
// Use [NewPacketConnDecoder] to create instances.
type PacketConnDecoder struct {
	r net.PacketConn // The underlying packet connection.
	n int64          // Max datagram size limit (0 means use DefaultPacketBufferSize).
	t time.Duration  // Idle timeout duration (0 means no timeout).
}

// NewPacketDecoder creates a new [PacketConnDecoder] for the given [net.PacketConn].
// This is the default [NewPacketDecoderFunc] used by [Server] when configured for
// packet-based protocols (e.g., "udp").
//
//nolint:ireturn // Implements NewPacketDecoderFunc interface.
func NewPacketDecoder(conn net.PacketConn) PacketDecoder {
	return NewPacketConnDecoder(conn)
}

// NewPacketConnDecoder creates and returns a new [*PacketConnDecoder] that reads
// packets from the provided [net.PacketConn].
func NewPacketConnDecoder(conn net.PacketConn) *PacketConnDecoder {
	return &PacketConnDecoder{r: conn}
}

// SetLimit configures the maximum allowed size (in bytes) for an incoming datagram.
// If a datagram larger than n bytes is received, [PacketConnDecoder.DecodeFrom]
// might truncate it or return an error depending on the underlying OS behavior,
// but this decoder primarily uses it to size its internal read buffer.
// If n is 0 or less, [DefaultPacketBufferSize] is used.
//
// Example:
//
//	dec := NewPacketConnDecoder(conn)
//	dec.SetLimit(1500) // Limit buffer to typical Ethernet MTU
func (i *PacketConnDecoder) SetLimit(n int64) {
	i.n = n
}

// SetIdleTimeout configures an idle timeout for the [PacketConnDecoder.DecodeFrom] operation.
// If no packet is received for the duration d, the ongoing DecodeFrom call will be
// interrupted by setting a past read deadline on the underlying [net.PacketConn].
//
// A duration of 0 or less disables the idle timeout.
//
// Example:
//
//	dec := NewPacketConnDecoder(conn)
//	dec.SetIdleTimeout(60 * time.Second) // Set a 60-second idle timeout
func (i *PacketConnDecoder) SetIdleTimeout(d time.Duration) {
	i.t = d
}

// DecodeFrom reads a single datagram from the underlying [net.PacketConn],
// decodes the JSON payload into v, and returns the sender's address.
// It respects the configured read limit ([PacketConnDecoder.SetLimit]) for buffer sizing
// and the idle timeout ([PacketConnDecoder.SetIdleTimeout]) using read deadlines.
// The provided context can also be used for cancellation.
func (i *PacketConnDecoder) DecodeFrom(ctx context.Context, v any) (net.Addr, error) {
	var dctx context.Context

	var stop context.CancelFunc

	if i.t > 0 {
		// With idle timeout
		dctx, stop = context.WithTimeout(ctx, i.t)
	} else {
		dctx, stop = context.WithCancel(ctx)
	}

	defer stop()

	// Reset deadline if it had fired
	if err := i.r.SetReadDeadline(time.Time{}); err != nil {
		return nil, err
	}

	readSize := i.n

	if i.n <= 0 {
		readSize = DefaultPacketBufferSize
	}

	buf := make([]byte, readSize)

	var wg sync.WaitGroup

	wg.Add(1)

	after := context.AfterFunc(dctx, func() {
		defer wg.Done()

		_ = i.r.SetReadDeadline(time.Now())
	})

	read, addr, err := i.r.ReadFrom(buf)

	if !after() {
		defer wg.Wait()
		return addr, errors.Join(err, dctx.Err())
	}

	if err != nil {
		return addr, err
	}

	return addr, i.Unmarshal(buf[:read], v)
}

// Unmarshal decodes a single JSON object from the provided byte slice into v.
// This method implements the [PacketDecoder] interface requirement and is goroutine-safe.
// It uses the global [Unmarshal] function variable (which defaults to [encoding/json.Unmarshal]).
func (i *PacketConnDecoder) Unmarshal(data []byte, v any) error {
	return Unmarshal(data, v)
}

// Close closes the underlying [net.PacketConn].
func (i *PacketConnDecoder) Close() error {
	return i.r.Close()
}
