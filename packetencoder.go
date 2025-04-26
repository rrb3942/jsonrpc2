package jsonrpc2

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"
)

// PacketEncoder defines the interface for encoding JSON-RPC messages to packet-based
// network connections like UDP ([net.PacketConn]). It's used by [Server] for
// sending responses or notifications over datagram protocols.
//
// Implementations are expected to encode a single JSON object or array per datagram.
// All methods must be safe for concurrent use by multiple goroutines.
type PacketEncoder interface {
	// EncodeTo marshals the value v into JSON, then sends it as a single datagram
	// to the specified network address `addr` using the underlying connection.
	// The context can be used for cancellation or deadlines, typically implemented
	// via [net.PacketConn.SetWriteDeadline].
	EncodeTo(ctx context.Context, v any, addr net.Addr) error
}

// NewPacketEncoderFunc defines the signature for functions that create a new [PacketEncoder].
// This allows customizing the packet encoder used by [Server].
type NewPacketEncoderFunc func(conn net.PacketConn) PacketEncoder

// PacketConnEncoder provides a default implementation of [PacketEncoder] for use
// with [net.PacketConn]. It marshals Go types to JSON and sends them as datagrams,
// supporting configurable idle timeouts for write operations.
// Use [NewPacketConnEncoder] to create instances.
type PacketConnEncoder struct {
	w net.PacketConn // The underlying packet connection.
	t time.Duration  // Idle timeout duration (0 means no timeout).
}

// NewPacketEncoder creates a new [PacketConnEncoder] for the given [net.PacketConn].
// This is the default [NewPacketEncoderFunc] used by [Server] when configured for
// packet-based protocols (e.g., "udp").
//
//nolint:ireturn // Implements NewPacketEncoderFunc interface.
func NewPacketEncoder(w net.PacketConn) PacketEncoder {
	return NewPacketConnEncoder(w)
}

// NewPacketConnEncoder creates and returns a new [*PacketConnEncoder] that sends
// packets via the provided [net.PacketConn].
func NewPacketConnEncoder(w net.PacketConn) *PacketConnEncoder {
	return &PacketConnEncoder{w: w}
}

// SetIdleTimeout configures an idle timeout for the [PacketConnEncoder.EncodeTo] operation.
// If sending the packet (writing to the connection) takes longer than duration d,
// the ongoing EncodeTo call will be interrupted by setting a past write deadline
// on the underlying [net.PacketConn].
//
// A duration of 0 or less disables the idle timeout.
//
// Example:
//
//	enc := NewPacketConnEncoder(conn)
//	enc.SetIdleTimeout(5 * time.Second) // Set a 5-second write timeout
func (i *PacketConnEncoder) SetIdleTimeout(d time.Duration) {
	i.t = d
}

// EncodeTo marshals v to JSON and sends it as a datagram to the target address `addr`.
// It respects the configured idle timeout ([PacketConnEncoder.SetIdleTimeout]) using write deadlines
// and uses the provided context for cancellation. This method is goroutine-safe.
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

	buf, err := Marshal(v)

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

// Close closes the underlying [net.PacketConn].
func (i *PacketConnEncoder) Close() error {
	return i.w.Close()
}
