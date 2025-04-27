package jsonrpc2

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
)

var (
	// Used for checking for json errors.
	errSyntax   = &json.SyntaxError{}
	errJSONType = &json.UnmarshalTypeError{}
)

// RPCServer represents a single JSON-RPC 2.0 server instance that handles requests
// and notifications over a specific transport (like a single network connection or
// packet stream). It reads requests using an internal decoder (wrapping [Decoder] or
// [PacketDecoder]), processes them using the configured [Handler], and writes
// responses using an internal encoder (wrapping [Encoder] or [PacketEncoder]).
//
// Instances are typically created by a [Server] for each incoming connection or
// managed manually for specific use cases. Use constructor functions like
// [NewStreamServer] or [NewPacketServer] to create instances.
//
// # Configuration
//
// After creating an RPCServer using a constructor, you can customize its behavior:
//   - Callbacks: Set fields in the [Callbacks] struct to handle events like decoding
//     errors, encoding errors, or panics within handlers. A default panic handler is provided.
//   - Handler: The core logic that processes RPC methods. This is mandatory and
//     usually provided during construction. See [Handler].
//   - SerialBatch: If true, processes requests within a single JSON-RPC batch sequentially
//     in the order received. If false (default), processes them concurrently using goroutines.
//   - NoRoutines: If true, processes each incoming request or batch synchronously within
//     the main [RPCServer.Run] loop. If false (default), spawns a new goroutine for each
//     request or batch. Batch members may still be processed concurrently based on SerialBatch.
//   - WaitOnClose: If true, when [RPCServer.Run] is shutting down (due to context
//     cancellation or connection error), it waits for all active request-handling
//     goroutines to complete before returning. If false (default), it cancels the
//     internal context immediately and then waits.
//
// # Context Values
//
// Within the [Handler.Handle] method, the context will contain:
//   - [CtxRPCServer]: The [*RPCServer] instance handling the request.
//   - [CtxFromAddr]: For packet-based servers ([NewPacketServer]), the sender's [net.Addr].
//   - Other values from the parent context passed to [RPCServer.Run].
type RPCServer struct {
	// Callbacks provides hooks for customizing server behavior on events like errors or panics.
	// See the [Callbacks] type documentation for details on available hooks.
	Callbacks Callbacks
	decoder   PacketDecoder // Internal decoder (stream or packet).
	// Handler is the core logic implementation for processing JSON-RPC requests.
	// This field is mandatory and must be non-nil.
	Handler Handler
	encoder PacketEncoder // Internal encoder (stream or packet).
	// SerialBatch controls whether requests within a JSON-RPC batch array ([])
	// are processed sequentially (true) or concurrently using goroutines (false).
	// Default is false (concurrent).
	SerialBatch bool
	// NoRoutines controls whether each incoming request or batch is processed
	// synchronously within the main server loop (true) or in its own goroutine (false).
	// Default is false (use goroutines).
	NoRoutines bool
	// WaitOnClose determines the shutdown behavior when Run exits.
	// If true, Run waits for all active request goroutines to complete before returning.
	// If false (default), it signals cancellation to active handlers immediately
	// via the context passed to Handle, and then waits.
	WaitOnClose bool
}

// newRPCServer is an internal helper to create RPCServer instances,
// handling the wrapping of stream/packet encoders/decoders.
func newRPCServer(d, e any, handler Handler) *RPCServer {
	var dec PacketDecoder

	var enc PacketEncoder

	switch v := d.(type) {
	case Decoder:
		dec = &decoderShim{v}
	case PacketDecoder:
		dec = v
	}

	switch v := e.(type) {
	case Encoder:
		enc = &encoderShim{v}
	case PacketEncoder:
		enc = v
	}

	rp := &RPCServer{decoder: dec, encoder: enc, Handler: handler}
	rp.Callbacks.OnHandlerPanic = DefaultOnHandlerPanic

	return rp
}

// NewStreamServer creates a new [*RPCServer] configured to operate over a
// stream-oriented transport (like TCP or Unix sockets) using the provided
// [Decoder] and [Encoder]. The handler is used to process incoming requests.
//
// Example:
//
//	conn, err := net.Dial("tcp", "localhost:9090")
//	// ... handle error ...
//	defer conn.Close()
//
//	mux := jsonrpc2.NewMethodMux()
//	// ... register methods on mux ...
//
//	server := jsonrpc2.NewStreamServer(jsonrpc2.NewDecoder(conn), jsonrpc2.NewEncoder(conn), mux)
//	// server.NoRoutines = true // Optional configuration
//
//	// Run the server (typically managed by a jsonrpc2.Server)
//	// err = server.Run(context.Background())
//	// ... handle error ...
func NewStreamServer(d Decoder, e Encoder, handler Handler) *RPCServer {
	return newRPCServer(d, e, handler)
}

// NewStreamServerFromIO creates a new [*RPCServer] configured for stream-oriented
// transports, using the provided [io.ReadWriter] (like a [net.Conn]).
// It automatically wraps rw with the default stream [NewDecoder] and [NewEncoder].
//
// Example:
//
//	conn, err := net.Dial("tcp", "localhost:9090")
//	// ... handle error ...
//	defer conn.Close()
//
//	mux := jsonrpc2.NewMethodMux()
//	// ... register methods on mux ...
//
//	server := jsonrpc2.NewStreamServerFromIO(conn, mux)
//	// Run the server...
func NewStreamServerFromIO(rw io.ReadWriter, handler Handler) *RPCServer {
	return NewStreamServer(NewDecoder(rw), NewEncoder(rw), handler)
}

// NewPacketServer creates a new [*RPCServer] configured to operate over a
// packet-oriented transport (like UDP or Unix datagram sockets) using the provided
// [PacketDecoder] and [PacketEncoder]. The handler processes incoming requests.
//
// Example:
//
//	conn, err := net.ListenPacket("udp", ":9091")
//	// ... handle error ...
//	defer conn.Close()
//
//	mux := jsonrpc2.NewMethodMux()
//	// ... register methods on mux ...
//
//	decoder := jsonrpc2.NewPacketDecoder(conn)
//	encoder := jsonrpc2.NewPacketEncoder(conn)
//	server := jsonrpc2.NewPacketServer(decoder, encoder, mux)
//	// Run the server...
//	err := server.Runc(ctx)
func NewPacketServer(d PacketDecoder, e PacketEncoder, handler Handler) *RPCServer {
	return newRPCServer(d, e, handler)
}

// NewRPCServerFromPacket creates a new [*RPCServer] configured for packet-oriented
// transports, using the provided [net.PacketConn].
// It automatically wraps rw with the default [NewPacketDecoder] and [NewPacketEncoder].
//
// Example:
//
//	conn, err := net.ListenPacket("udp", ":9091")
//	// ... handle error ...
//	defer conn.Close()
//
//	mux := jsonrpc2.NewMethodMux()
//	// ... register methods on mux ...
//
//	server := jsonrpc2.NewRPCServerFromPacket(conn, mux)
//	// Run the server...
func NewRPCServerFromPacket(rw net.PacketConn, handler Handler) *RPCServer {
	return NewPacketServer(NewPacketDecoder(rw), NewPacketEncoder(rw), handler)
}

// handleRequest processes a single raw JSON-RPC request message.
// It unmarshals the request, calls the handler, and prepares the response object.
// It handles panics from the handler and converts errors/results into Response objects.
// Returns nil for notifications.
func (rp *RPCServer) handleRequest(ctx context.Context, rpc json.RawMessage) (res any) {
	var req Request

	err := rp.decoder.Unmarshal(rpc, &req)

	if err != nil {
		rp.Callbacks.runOnDecodingError(ctx, rpc, err)

		return &Response{ID: NewNullID(), Error: ErrInvalidRequest.WithData(err.Error())}
	}

	// Catch panics from inside the handler
	defer func() {
		if r := recover(); r != nil {
			res = req.ResponseWithError(ErrInternalError)

			rp.Callbacks.runOnHandlerPanic(ctx, &req, r)
		}
	}()

	result, err := rp.Handler.Handle(ctx, &req)

	if req.IsNotification() {
		return nil
	}

	if err != nil {
		return req.ResponseWithError(err)
	}

	// Special return types
	switch r := result.(type) {
	case *Response:
		return r
	case RawResponse:
		return json.RawMessage(r)
	}

	if result == nil {
		result = nullValue
	}

	return req.ResponseWithResult(result)
}

// runRequest handles a single JSON object request.
// It calls handleRequest and sends the response if one is generated.
func (rp *RPCServer) runRequest(ctx context.Context, r json.RawMessage, from net.Addr) {
	if resp := rp.handleRequest(ctx, r); resp != nil {
		if err := rp.encoder.EncodeTo(ctx, resp, from); err != nil {
			rp.Callbacks.runOnEncodingError(ctx, resp, err)
		}
	}
}

// runRequests handles a JSON array (batch) of requests.
// It unmarshals the array, then processes each request either serially or concurrently
// based on the SerialBatch and NoRoutines settings. Finally, it sends back any
// generated responses as a JSON array.
func (rp *RPCServer) runRequests(ctx context.Context, raw json.RawMessage, from net.Addr) {
	var objs []json.RawMessage

	var resps []any

	// Split into individual objects
	err := rp.decoder.Unmarshal(raw, &objs)

	if err != nil {
		rp.Callbacks.runOnDecodingError(ctx, raw, err)

		_ = rp.encoder.EncodeTo(ctx, &Response{ID: NewNullID(), Error: ErrInvalidRequest.WithData(err.Error())}, from)

		return
	}

	// Handle empty array case
	if len(objs) == 0 {
		resp := &Response{ID: NewNullID(), Error: ErrInvalidRequest}
		if err := rp.encoder.EncodeTo(ctx, resp, from); err != nil {
			rp.Callbacks.runOnEncodingError(ctx, resp, err)
		}

		return
	}

	resps = make([]any, 0, len(objs))

	if len(objs) == 1 || rp.SerialBatch {
		for _, jReq := range objs {
			resp := rp.handleRequest(ctx, jReq)

			if resp != nil {
				resps = append(resps, resp)
			}
		}
	} else {
		var wg sync.WaitGroup

		var respMu sync.Mutex

		for _, jReq := range objs {
			wg.Add(1)

			go func(gctx context.Context, jr json.RawMessage) {
				defer wg.Done()

				if res := rp.handleRequest(gctx, jr); res != nil {
					respMu.Lock()
					defer respMu.Unlock()

					resps = append(resps, res)
				}
			}(ctx, jReq)
		}

		wg.Wait()
	}

	if len(resps) > 0 {
		if err := rp.encoder.EncodeTo(ctx, resps, from); err != nil {
			rp.Callbacks.runOnEncodingError(ctx, resps, err)
		}
	}
}

// run determines if the incoming message is a single request or a batch
// and dispatches it to runRequest or runRequests accordingly.
// It adds CtxFromAddr to the context.
func (rp *RPCServer) run(ctx context.Context, buf json.RawMessage, from net.Addr) {
	// Add sender address to context if available (primarily for packet servers).
	if from != nil {
		ctx = context.WithValue(ctx, CtxFromAddr, from)
	}

	switch jsonHintType(buf) {
	case TypeArray:
		rp.runRequests(ctx, buf, from)
	case TypeObject:
		rp.runRequest(ctx, buf, from)
	default:
		// Invalid JSON structure (neither object nor array).
		// Send a Parse Error response.
		resp := NewResponseError(ErrParseError)
		if err := rp.encoder.EncodeTo(ctx, resp, from); err != nil {
			rp.Callbacks.runOnEncodingError(ctx, resp, err)
		}
		// Also notify via callback if configured.
		rp.Callbacks.runOnDecodingError(ctx, buf, ErrParseError)
	}
}

// Close attempts to close the underlying decoder and encoder if they implement [io.Closer].
// This is typically used to release resources like network connections or files
// associated with the transport. Errors from closing both are joined together.
func (rp *RPCServer) Close() error {
	var err error

	// Close decoder if possible.
	if dc, ok := rp.decoder.(io.Closer); ok {
		err = dc.Close()
	}

	// Close encoder if possible, joining errors.

	if ec, ok := rp.encoder.(io.Closer); ok {
		err = errors.Join(err, ec.Close())
	}

	return err
}

// Run starts the main loop of the RPC server. It continuously reads messages
// from the decoder, processes them, and sends responses via the encoder.
//
// It blocks until the provided context `ctx` is cancelled, an unrecoverable error
// occurs during decoding (like [io.EOF] or a connection error), or a fatal
// JSON syntax error is encountered on a stream-based transport.
//
// Error Handling:
//   - [io.EOF] or context cancellation errors generally indicate a clean shutdown
//     and are returned after cleanup.
//   - JSON syntax errors ([json.SyntaxError]): A JSON-RPC Parse Error response is sent.
//     For stream-based decoders, Run returns immediately as the stream state is uncertain.
//     For packet-based decoders, Run continues processing subsequent packets.
//   - JSON type errors ([json.UnmarshalTypeError]): A JSON-RPC Invalid Request error
//     response is sent. Run continues processing as the stream/packet state is likely recoverable.
//   - Other decoding errors: Run returns the error.
//
// Shutdown:
// When Run exits, it performs cleanup:
//  1. Waits for active goroutines according to `WaitOnClose`.
//  2. Cancels the internal server context (`sctx`).
//  3. Calls [RPCServer.Close] to close the underlying encoder/decoder if possible.
//  4. Calls the [Callbacks.OnExit] callback.
//  5. Returns any error encountered during shutdown or the initial error causing exit, joined together.
func (rp *RPCServer) Run(ctx context.Context) (err error) {
	var wg sync.WaitGroup

	// Create an internal context derived from the input context.
	// Add the RPCServer instance to this context.
	sctx, stop := context.WithCancel(context.WithValue(ctx, CtxRPCServer, rp))

	// Defer cleanup actions.
	defer func() {
		// Manage shutdown sequence based on WaitOnClose.
		if rp.WaitOnClose {
			wg.Wait() // Wait for handlers first.
			stop()    // Then cancel context.
		} else {
			stop()    // Cancel context first.
			wg.Wait() // Then wait for handlers.
		}

		// Join potential errors: initial error, context error, close error.
		// Note: ctx.Err() might be nil if Run exited due to non-context error.
		err = errors.Join(err, ctx.Err(), rp.Close())

		// Call the OnExit callback.
		rp.Callbacks.runOnExit(ctx, err) // Pass original context.
	}()

	// Main loop: Read and process messages.
	for {
		var from net.Addr

		var buf json.RawMessage

		// Decode the next message. DecodeFrom handles context cancellation/timeouts internally.
		from, err = rp.decoder.DecodeFrom(sctx, &buf)

		if err != nil {
			// Handle specific JSON parsing errors.
			if errors.As(err, &errSyntax) {
				// Send Parse Error response.
				_ = rp.encoder.EncodeTo(sctx, NewResponseError(ErrParseError.WithData(err.Error())), from) // Use sctx for encoding attempt.
				rp.Callbacks.runOnDecodingError(sctx, buf, err)

				// For stream decoders, a syntax error corrupts the stream, so we must exit.
				if _, ok := rp.decoder.(*decoderShim); ok {
					return // Return the syntax error.
				}
				// For packet decoders, we can potentially recover and process the next packet.
				err = nil // Clear error to continue loop.

				continue
			}

			if errors.As(err, &errJSONType) {
				// Send Invalid Request error response.
				_ = rp.encoder.EncodeTo(sctx, NewResponseError(ErrInvalidRequest.WithData(err.Error())), from)
				rp.Callbacks.runOnDecodingError(sctx, buf, err)

				// Type errors usually don't corrupt the stream/packet flow, so continue.
				err = nil // Clear error to continue loop.

				continue
			}

			// For other errors (EOF, context cancelled, connection closed, etc.), exit the loop.
			return // Return the error that caused the exit.
		}

		// Ignore empty messages (might happen with certain decoders/transports).
		if len(buf) == 0 {
			continue
		}

		// Process the message, either synchronously or in a goroutine.
		if rp.NoRoutines {
			rp.run(sctx, buf, from) // Run synchronously in the loop.
		} else {
			wg.Add(1)

			go func(gctx context.Context, gbuf json.RawMessage, gfrom net.Addr) {
				defer wg.Done()
				rp.run(gctx, gbuf, gfrom) // Run in a goroutine with the server context.
			}(sctx, buf, from)
		}
	}
}
