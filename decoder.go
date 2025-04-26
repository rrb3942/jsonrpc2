package jsonrpc2

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

var (
	ErrDecoding     = errors.New("jsonrpc2: decoding error")
	ErrJSONTooLarge = errors.New("jsonrpc2: JSON payload larger than configured read limit")
)

// Decoder defines the interface for decoding JSON-RPC messages.
// It is used by [Client] and [Server] to process incoming JSON data.
//
// A compatible implementation must handle standard JSON decoding, including
// types like [json.Marshaler], [json.Unmarshaler], [json.RawMessage], and [json.Number].
// Support for the `omitzero` struct tag (Go 1.24+) is also required.
//
// Implementations are encouraged to support [io.Closer] for resource cleanup
// and [DeadlineReader] for timeout handling, if applicable to the underlying transport.
type Decoder interface {
	// Decode reads the next JSON-encoded value from its input and stores it
	// in the value pointed to by v. The context can be used for cancellation
	// or deadlines.
	Decode(ctx context.Context, v any) error

	// Unmarshal parses the JSON-encoded data and stores the result
	// in the value pointed to by v. This method must be safe for concurrent use
	// by multiple goroutines, as it might be used independently of Decode
	// (e.g., in packet-based transports).
	Unmarshal(data []byte, v any) error
}

// NewDecoderFunc defines the signature for functions that create a new [Decoder].
// This allows customizing the decoder used by [Server] or [Client].
type NewDecoderFunc func(r io.Reader) Decoder

// StreamDecoder provides a stream-based [Decoder] implementation.
// It reads JSON objects sequentially from an [io.Reader].
// Use [NewStreamDecoder] to create instances.
// It supports optional read limits via [StreamDecoder.SetLimit] and idle timeouts
// via [StreamDecoder.SetIdleTimeout].
type StreamDecoder struct {
	r  io.Reader       // Underlying reader
	lr *io.LimitedReader // Used when SetLimit is called
	d  JSONDecoder     // The actual JSON decoder (e.g., from encoding/json)
	n  int64           // Read limit in bytes (0 means no limit)
	t  time.Duration   // Idle timeout duration (0 means no timeout)
}

// NewDecoder creates a new [StreamDecoder] that reads from r.
// This is the default [NewDecoderFunc] used by [Server] if no custom
// function is provided.
//
//nolint:ireturn // Implements NewDecoderFunc interface.
func NewDecoder(r io.Reader) Decoder {
	return NewStreamDecoder(r)
}

// NewStreamDecoder creates and returns a new [*StreamDecoder] that reads from r.
// It initializes the decoder with a standard JSON decoder.
func NewStreamDecoder(r io.Reader) *StreamDecoder {
	// Initialize with the default JSON decoder function.
	return &StreamDecoder{r: r, d: NewJSONDecoder(r)}
}

// SetLimit configures a maximum number of bytes (n) to read for a single JSON object
// during a [StreamDecoder.Decode] call. If a JSON object exceeds this limit,
// [StreamDecoder.Decode] will return [ErrJSONTooLarge].
// A limit of 0 or less disables the read limit.
//
// Example:
//
//	dec := NewStreamDecoder(conn)
//	dec.SetLimit(1024 * 1024) // Limit reads to 1 MiB per JSON object
func (i *StreamDecoder) SetLimit(n int64) {
	i.n = n
	if n > 0 {
		// If a limit is set, wrap the reader with io.LimitedReader.
		i.lr = &io.LimitedReader{R: i.r, N: i.n}
		// Use the limited reader for the JSON decoder.
		i.d = NewJSONDecoder(i.lr)
	} else {
		// If limit is removed, revert to the original reader.
		i.lr = nil
		i.d = NewJSONDecoder(i.r)
	}
}

// SetIdleTimeout configures an idle timeout for the [StreamDecoder.Decode] operation.
// If no data is received for the duration d, the ongoing Decode call may be interrupted.
//
// Timeout mechanism depends on the underlying [io.Reader]:
//   - If the reader implements [DeadlineReader] (like [net.Conn]), its SetReadDeadline method is used.
//     This is the preferred mechanism as it doesn't necessarily close the connection.
//   - If the reader implements [io.Closer] but not [DeadlineReader], its Close method is called
//     upon timeout to interrupt the blocking read.
//   - If the reader implements neither, timeouts are not supported by this method.
//
// A duration of 0 or less disables the idle timeout.
//
// Example:
//
//	dec := NewStreamDecoder(conn)
//	dec.SetIdleTimeout(30 * time.Second) // Set a 30-second idle timeout
func (i *StreamDecoder) SetIdleTimeout(d time.Duration) {
	i.t = d
}

// ioErr checks for read limit errors.
// It translates specific io errors (EOF, ErrUnexpectedEOF) that occur when
// the read limit is hit into ErrJSONTooLarge.
func (i *StreamDecoder) ioErr(e error) error {
	// Check only if a limit is active (lr is not nil) and the reader hit the limit (N <= 0).
	if i.lr != nil && i.lr.N <= 0 {
		// The json decoder might return io.EOF or io.ErrUnexpectedEOF when it reads
		// exactly up to the limit or tries to read past it mid-object.
		if errors.Is(e, io.EOF) || errors.Is(e, io.ErrUnexpectedEOF) {
			return ErrJSONTooLarge
		}
	}
	// Otherwise, return the original error.
	return e
}

// cancelDecode handles the Decode logic when cancellation (via context) or
// idle timeouts are involved, utilizing DeadlineReader or io.Closer if available.
func (i *StreamDecoder) cancelDecode(ctx context.Context, cReader io.Closer, v any) error {
	var dctx context.Context
	var stop context.CancelFunc

	deadLiner, haveDeadline := cReader.(DeadlineReader)

	// If the reader supports deadlines, ensure any previous deadline is cleared.
	if haveDeadline {
		// A zero time value clears the deadline.
		if err := deadLiner.SetReadDeadline(time.Time{}); err != nil {
			// Failing to clear the deadline is problematic.
			return err // Or wrap error? Consider implications.
		}
	}

	// Set up the context for this decode operation, potentially with a timeout.
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
		// Prefer using SetReadDeadline to interrupt the read if available.
		if haveDeadline {
			// Setting a deadline in the past effectively cancels the read immediately.
			_ = deadLiner.SetReadDeadline(time.Now()) // Error ignored, best effort.
			return
		}
		// Fallback: If no DeadlineReader, close the reader to unblock Decode.
		_ = cReader.Close() // Error ignored, best effort.
	})

	// Perform the actual decoding.
	decodeErr := i.ioErr(i.d.Decode(v))

	// Try to stop the AfterFunc timer/goroutine. If it already ran, after() returns false.
	if !after() {
		// If AfterFunc already executed (timeout or cancellation happened),
		// wait for its goroutine to finish before proceeding.
		wg.Wait()
	}

	// Check if the context expired (timed out or cancelled).
	contextErr := dctx.Err()

	// If decoding failed, join the decode error with any context error.
	if decodeErr != nil {
		// errors.Join handles nil errors gracefully.
		return errors.Join(decodeErr, contextErr)
	}

	// If decoding succeeded, still return any context error (e.g., if cancelled just after success).
	return contextErr
}

// Decode reads the next JSON value from the stream and decodes it into v.
// It respects the configured read limit ([StreamDecoder.SetLimit]) and idle timeout
// ([StreamDecoder.SetIdleTimeout]). It uses the provided context for cancellation.
func (i *StreamDecoder) Decode(ctx context.Context, v any) error {
	// If a read limit is set, reset the counter for this Decode call.
	if i.lr != nil {
		i.lr.N = i.n
	}

	// Check if the underlying reader supports cancellation mechanisms.
	if c, ok := i.r.(io.Closer); ok {
		// Use cancelDecode which handles context cancellation and idle timeouts
		// using DeadlineReader or io.Closer.
		return i.cancelDecode(ctx, c, v)
	}

	// If the reader doesn't support Closer (and thus likely not DeadlineReader either),
	// we cannot implement context cancellation or idle timeouts effectively for blocking reads.
	// Perform a simple decode and check for read limit errors.
	// Note: This decode call might block indefinitely if the reader blocks.
	return i.ioErr(i.d.Decode(v))
}

// Unmarshal decodes a single JSON object from the provided byte slice into v.
// This method implements the [Decoder] interface requirement and is goroutine-safe.
// It uses the global [Unmarshal] function variable.
func (i *StreamDecoder) Unmarshal(data []byte, v any) error {
	// Delegates to the package-level Unmarshal variable, which defaults to json.Unmarshal.
	return Unmarshal(data, v)
}

// Close attempts to close the underlying [io.Reader] if it implements [io.Closer].
// This is useful for releasing resources like network connections or files.
// Returns nil if the reader does not implement [io.Closer].
func (i *StreamDecoder) Close() error {
	if c, ok := i.r.(io.Closer); ok {
		return c.Close()
	}
	// No Closer interface, nothing to close.
	return nil
}
