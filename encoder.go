package jsonrpc2

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

// ErrEncoding indicates a general error during the JSON encoding process.
// Specific underlying errors might be wrapped.
var ErrEncoding = errors.New("jsonrpc2: encoding error")

// Encoder defines the interface for encoding JSON-RPC messages.
// It is used by [Client] and [Server] to send JSON data.
//
// Implementations MUST be safe for concurrent use by multiple goroutines.
//
// A compatible implementation must handle standard JSON decoding, including types
// like json.Marshaler, json.Unmarshaler, json.RawMessage, and json.Number.
// Support for the `omitzero` struct tag (Go 1.24+) is also required.
//
// Implementations are encouraged to support [io.Closer] for resource cleanup
// and [DeadlineWriter] for timeout handling, if applicable to the underlying transport.
type Encoder interface {
	// Encode writes the JSON encoding of v to its output.
	// The context can be used for cancellation or deadlines.
	Encode(ctx context.Context, v any) error
}

// NewEncoderFunc defines the signature for functions that create a new [Encoder].
// This allows customizing the encoder used by [Server] or [Client].
type NewEncoderFunc func(w io.Writer) Encoder

// lockWriter wraps an io.Writer with a mutex to ensure thread-safe writes.
type lockWriter struct {
	w  io.Writer
	mu sync.Mutex
}

func (lw *lockWriter) Write(data []byte) (int, error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()

	return lw.w.Write(data)
}

// StreamEncoder provides a stream-based [Encoder] implementation.
// It writes JSON objects sequentially to an [io.Writer].
// Use [NewStreamEncoder] to create instances.
// It ensures thread-safe writes to the underlying writer and supports
// optional idle timeouts via [StreamEncoder.SetIdleTimeout].
type StreamEncoder struct {
	lw *lockWriter   // Internal lock-protected writer
	w  io.Writer     // Original underlying writer
	e  JSONEncoder   // The actual JSON encoder (e.g., from encoding/json)
	t  time.Duration // Idle timeout duration (0 means no timeout)
}

// NewEncoder creates a new [StreamEncoder] that writes to w.
// This is the default [NewEncoderFunc] used by [Server] if no custom
// function is provided. It ensures thread-safe writes.
//
//nolint:ireturn // Implements NewEncoderFunc interface.
func NewEncoder(w io.Writer) Encoder {
	return NewStreamEncoder(w)
}

// NewStreamEncoder creates and returns a new [*StreamEncoder] that writes to w.
// It wraps the writer to ensure thread-safety for concurrent [StreamEncoder.Encode] calls.
func NewStreamEncoder(w io.Writer) *StreamEncoder {
	// Wrap the writer in a lockWriter to synchronize access.
	lw := &lockWriter{w: w}
	// Initialize with the lockWriter and the default JSON encoder function.
	return &StreamEncoder{lw: lw, w: w, e: NewJSONEncoder(lw)}
}

// SetIdleTimeout configures an idle timeout for the [StreamEncoder.Encode] operation.
// If writing the JSON object takes longer than duration d, the ongoing Encode call
// may be interrupted.
//
// Timeout mechanism depends on the underlying [io.Writer]:
//   - If the writer implements [DeadlineWriter] (like [net.Conn]), its SetWriteDeadline method is used.
//     This is the preferred mechanism as it doesn't necessarily close the connection.
//   - If the writer implements [io.Closer] but not [DeadlineWriter], its Close method is called
//     upon timeout to interrupt the blocking write.
//   - If the writer implements neither, timeouts are not supported by this method.
//
// A duration of 0 or less disables the idle timeout.
//
// Example:
//
//	enc := NewStreamEncoder(conn)
//	enc.SetIdleTimeout(10 * time.Second) // Set a 10-second write timeout
func (i *StreamEncoder) SetIdleTimeout(d time.Duration) {
	i.t = d
}

// cancelEncode handles the Encode logic when cancellation (via context) or
// idle timeouts are involved, utilizing DeadlineWriter or io.Closer if available.
func (i *StreamEncoder) cancelEncode(ctx context.Context, cWriter io.Closer, v any) error {
	var dctx context.Context

	var stop context.CancelFunc

	deadLiner, haveDeadline := cWriter.(DeadlineWriter)

	// If the writer supports deadlines, ensure any previous deadline is cleared.
	if haveDeadline {
		// A zero time value clears the deadline.
		if err := deadLiner.SetWriteDeadline(time.Time{}); err != nil {
			return err // Or wrap error?
		}
	}

	// Set up the context for this encode operation, potentially with a timeout.
	if i.t > 0 {
		dctx, stop = context.WithTimeout(ctx, i.t)
	} else {
		// No idle timeout, just use the parent context for cancellation.
		dctx, stop = context.WithCancel(ctx)
	}

	defer stop() // Ensure resources associated with dctx are released.

	var wg sync.WaitGroup

	wg.Add(1) // Wait group to ensure the AfterFunc goroutine completes.

	// This function will execute if the context (dctx) is cancelled or times out.
	after := context.AfterFunc(dctx, func() {
		defer wg.Done()
		// Prefer using SetWriteDeadline to interrupt the write if available.
		if haveDeadline {
			// Setting a deadline in the past effectively cancels the write immediately.
			_ = deadLiner.SetWriteDeadline(time.Now()) // Error ignored, best effort.
			return
		}
		// Fallback: If no DeadlineWriter, close the writer to unblock Encode.
		_ = cWriter.Close() // Error ignored, best effort.
	})

	// Perform the actual encoding. This write is protected by the lockWriter's mutex.
	encodeErr := i.e.Encode(v)

	// Try to stop the AfterFunc timer/goroutine. If it already ran, after() returns false.
	if !after() {
		// If AfterFunc already executed (timeout or cancellation happened),
		// wait for its goroutine to finish before proceeding.
		wg.Wait()
	}

	// If encoding failed, join the encode error with any context error.
	if encodeErr != nil {
		// errors.Join handles nil errors gracefully.
		return errors.Join(encodeErr, dctx.Err())
	}

	return nil
}

// Encode writes the JSON encoding of v to the underlying stream.
// It respects the configured idle timeout ([StreamEncoder.SetIdleTimeout]) and uses
// the provided context for cancellation. This method is goroutine-safe due
// to internal locking.
func (i *StreamEncoder) Encode(ctx context.Context, v any) error {
	// Check if the underlying writer supports cancellation mechanisms (Closer or DeadlineWriter).
	// Note: The actual writer passed to cancelEncode might be the original writer `i.w`
	// or potentially the `lockWriter` if it also implements Closer/DeadlineWriter,
	// though typically the underlying connection (`i.w`) provides these.
	if c, ok := i.w.(io.Closer); ok {
		// Use cancelEncode which handles context cancellation and idle timeouts.
		return i.cancelEncode(ctx, c, v)
	}

	// If the writer doesn't support Closer (and thus likely not DeadlineWriter either),
	// we cannot implement context cancellation or idle timeouts effectively for blocking writes.
	// Perform a simple encode. The write itself is still thread-safe due to lockWriter.
	// Note: This encode call might block indefinitely if the writer blocks.
	return i.e.Encode(v)
}

// Close attempts to close the underlying [io.Writer] if it implements [io.Closer].
// This is useful for releasing resources like network connections or files.
// Returns nil if the writer does not implement [io.Closer].
func (i *StreamEncoder) Close() error {
	if c, ok := i.w.(io.Closer); ok {
		return c.Close()
	}
	// No Closer interface, nothing to close.
	return nil
}
