package jsonrpc2

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ContextKey defines the type for keys used to store values in a [context.Context].
// These keys are used by the Server and RPCServer to provide access to underlying
// connection details or request information within Handler functions.
type ContextKey int

const (
	// CtxNetConn is the context key for the underlying [net.Conn] in stream-based servers ([Server.Serve]).
	CtxNetConn ContextKey = iota
	// CtxNetPacketConn is the context key for the underlying [net.PacketConn] in packet-based servers ([Server.ServePacket]).
	CtxNetPacketConn
	// CtxHTTPRequest is the context key for the [*http.Request] when using [HTTPHandler] or [Server.ListenAndServe] with an "http" scheme.
	CtxHTTPRequest
	// CtxFromAddr is the context key for the sender's [net.Addr] in packet-based servers ([RPCServer] used by [Server.ServePacket]).
	CtxFromAddr
	// CtxRPCServer is the context key for the parent [*RPCServer] handling the current request or connection.
	CtxRPCServer
)

const (
	// DefaultHTTPReadTimeout specifies the default [http.Server.ReadHeaderTimeout] (5 seconds).
	DefaultHTTPReadTimeout = 5
	// DefaultHTTPShutdownTimeout specifies the default graceful shutdown timeout (30 seconds) for the HTTP server.
	DefaultHTTPShutdownTimeout = 30
)

// ErrUnknownScheme indicates that the URI provided to [Server.ListenAndServe] has a scheme
// that is not supported (e.g., "ftp").
var ErrUnknownScheme = errors.New("jsonrpc2: unknown scheme in uri")

// Server manages listening for incoming JSON-RPC 2.0 connections or packets
// and dispatching them to a configured [Handler]. It simplifies setting up
// listeners for various network protocols (TCP, UDP, Unix sockets, HTTP).
//
// For stream-based protocols (TCP, Unix), it accepts connections and creates
// a dedicated [*RPCServer] instance and goroutine for each connection.
// For packet-based protocols (UDP, Unixgram), it typically uses multiple
// goroutines (controlled by PacketRoutines) reading from a single [net.PacketConn],
// each served by an [*RPCServer].
// For HTTP, it sets up an [net/http.Server] using an [HTTPHandler].
//
// Use [NewServer] to create instances. Configuration options like custom
// encoders/decoders ([NewEncoderFunc], [NewDecoderFunc], etc.), a [Binder],
// HTTP timeouts, and the number of packet processing routines can be set
// on the Server instance after creation.
type Server struct {
	// handler is the core [Handler] responsible for processing incoming RPC requests.
	// This is mandatory and set via NewServer.
	handler Handler

	// Binder allows optional binding of the RPC server lifecycle or context,
	// called just before an RPCServer starts processing requests for a connection or packet stream.
	Binder Binder

	// NewEncoder specifies the function to create a stream-based JSON encoder.
	// Defaults to [NewEncoder].
	NewEncoder NewEncoderFunc
	// NewDecoder specifies the function to create a stream-based JSON decoder.
	// Defaults to [NewDecoder].
	NewDecoder NewDecoderFunc

	// NewPacketEncoder specifies the function to create a packet-based JSON encoder.
	// Defaults to [NewPacketEncoder]. Used by ServePacket.
	NewPacketEncoder NewPacketEncoderFunc
	// NewPacketDecoder specifies the function to create a packet-based JSON decoder.
	// Defaults to [NewPacketDecoder]. Used by ServePacket.
	NewPacketDecoder NewPacketDecoderFunc

	// HTTPReadTimeout sets the ReadHeaderTimeout for the internal http.Server
	// when using ListenAndServe with an "http" scheme. Defaults to DefaultHTTPReadTimeout seconds.
	HTTPReadTimeout time.Duration
	// HTTPShutdownTimeout sets the timeout for graceful shutdown of the internal http.Server.
	// Defaults to DefaultHTTPShutdownTimeout seconds.
	HTTPShutdownTimeout time.Duration
	// PacketRoutines specifies the number of goroutines to spawn for processing packets
	// when using ServePacket or ListenAndServe with UDP/Unixgram schemes.
	// Defaults to min(runtime.NumCPU(), runtime.GOMAXPROCS()).
	PacketRoutines int
}

// NewServer creates a new [*Server] configured with the provided [Handler].
// It initializes the server with default encoder/decoder functions (e.g., [NewEncoder]),
// default HTTP timeouts, and a default number of packet processing routines based on CPU cores.
// These defaults can be overridden by modifying the fields of the returned Server instance
// before calling listening methods like [Server.ListenAndServe] or [Server.Serve].
//
// Example:
//
//	mux := jsonrpc2.NewMethodMux()
//	// ... register methods on mux ...
//	server := jsonrpc2.NewServer(mux)
//	// Optionally customize:
//	// server.HTTPShutdownTimeout = 60 * time.Second
//	// server.PacketRoutines = 8
//	// server.Binder = myCustomBinder
//	err := server.ListenAndServe(context.Background(), "tcp::9090")
//	// ... handle error ...
func NewServer(handler Handler) *Server {
	var server Server

	server.handler = handler
	// Set default encoder/decoder functions
	server.NewEncoder = NewEncoder
	server.NewDecoder = NewDecoder
	server.NewPacketEncoder = NewPacketEncoder
	server.NewPacketDecoder = NewPacketDecoder
	// Set default timeouts
	server.HTTPReadTimeout = time.Duration(DefaultHTTPReadTimeout) * time.Second
	server.HTTPShutdownTimeout = time.Duration(DefaultHTTPShutdownTimeout) * time.Second
	// Set default packet routines
	server.PacketRoutines = min(runtime.NumCPU(), runtime.GOMAXPROCS(-1))

	return &server
}

// ListenAndServe parses the listenURI, determines the protocol, and starts listening
// for incoming connections or packets, serving them using the configured handler.
// It blocks until the provided context is cancelled or an unrecoverable error occurs.
//
// Supported URI schemes:
//   - tcp, tcp4, tcp6: Listens on a TCP address (e.g., "tcp:127.0.0.1:9090", "tcp::9090"). Delegates to [Server.Serve].
//   - unix: Listens on a Unix domain socket (e.g., "unix:///tmp/mysocket.sock"). Delegates to [Server.Serve].
//   - udp, udp4, udp6: Listens on a UDP address (e.g., "udp::9091"). Delegates to [Server.ServePacket].
//   - unixgram: Listens on a Unix domain datagram socket (e.g., "unixgram:///tmp/mydgram.sock"). Delegates to [Server.ServePacket].
//   - http: Starts an HTTP server (e.g., "http://localhost:8080/rpc"). The path component (/rpc) is used for the handler. Delegates to internal listenAndServeHTTP.
//
// The server gracefully shuts down when the context is cancelled. For HTTP servers,
// shutdown respects the [Server.HTTPShutdownTimeout]. For other listeners, cancellation
// closes the listener, allowing active connections/goroutines to finish processing.
//
// It is safe to call ListenAndServe multiple times concurrently with non-conflicting listening URIs.
//
// Example:
//
//	ctx, cancel := context.WithCancel(context.Background())
//	defer cancel()
//
//	mux := jsonrpc2.NewMethodMux()
//	// ... register methods ...
//	server := jsonrpc2.NewServer(mux)
//
//	var wg sync.WaitGroup
//
//	wg.Add(1)
//	go func() {
//	    defer wg.Done()
//	    log.Println("Starting TCP server on :9090")
//	    if err := server.ListenAndServe(ctx, "tcp::9090"); err != nil && !errors.Is(err, net.ErrClosed) {
//	        log.Printf("TCP server error: %v", err)
//	    }
//	}()
//
//	wg.Add(1)
//	go func() {
//	    defer wg.Done()
//	    log.Println("Starting HTTP server on :8080/rpc")
//	    if err := server.ListenAndServe(ctx, "http://:8080/rpc"); err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, http.ErrServerClosed) {
//	        log.Printf("HTTP server error: %v", err)
//	    }
//	}()
//
//	// Wait for interrupt signal to cancel context and shut down servers
//	sigChan := make(chan os.Signal, 1)
//	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
//	<-sigChan
//	log.Println("Shutting down servers...")
//	cancel()
//	// Wait for servers to stop
//	wg.Wait()
func (s *Server) ListenAndServe(ctx context.Context, listenURI string) error {
	uri, err := url.Parse(listenURI)

	if err != nil {
		return err
	}

	switch uri.Scheme {
	case "tcp", "tcp4", "tcp6":
		return s.listenAndServe(ctx, uri.Scheme, strings.TrimPrefix(listenURI, uri.Scheme+":"))
	case "unix":
		return s.listenAndServe(ctx, uri.Scheme, uri.Path)
	case "udp", "udp4", "udp6":
		return s.listenAndServePacket(ctx, uri.Scheme, strings.TrimPrefix(listenURI, uri.Scheme+":"))
	case "unixgram", "unixpacket":
		return s.listenAndServePacket(ctx, uri.Scheme, uri.Path)
	case "http":
		return s.listenAndServeHTTP(ctx, uri)
	}

	return ErrUnknownScheme
}

// listenAndServe is an internal helper that listens on a stream-oriented network (TCP, Unix)
// and then calls Serve.
func (s *Server) listenAndServe(ctx context.Context, network, addr string) error {
	ln, err := net.Listen(network, addr)

	if err != nil {
		return err
	}

	return s.Serve(ctx, ln)
}

// listenAndServePacket is an internal helper that listens on a packet-oriented network (UDP, Unixgram)
// and then calls ServePacket.
func (s *Server) listenAndServePacket(ctx context.Context, network, addr string) error {
	pc, err := net.ListenPacket(network, addr)

	if err != nil {
		return err
	}

	return s.ServePacket(ctx, pc)
}

// listenAndServeHTTP is an internal helper that sets up and runs an [net/http.Server]
// based on the provided URI. It configures an [HTTPHandler] with the Server's
// settings (Handler, Binder, Encoders/Decoders) and uses the Server's HTTP timeouts.
// It handles graceful shutdown when the context is cancelled.
func (s *Server) listenAndServeHTTP(ctx context.Context, uri *url.URL) error {
	shutdownCtx, stop := context.WithCancel(ctx)
	defer stop()

	path := uri.Path

	if path == "" {
		path = "/"
	}

	httpMux := http.NewServeMux()

	handler := NewHTTPHandler(s.handler)
	handler.Binder = s.Binder
	handler.NewDecoder = s.NewDecoder
	handler.NewEncoder = s.NewEncoder

	httpMux.Handle(path, handler)

	httpServer := &http.Server{
		Addr:              uri.Host,
		Handler:           httpMux,
		ReadHeaderTimeout: s.HTTPReadTimeout,
	}

	// Goroutine to handle graceful shutdown when shutdownCtx is cancelled.
	go func() {
		<-shutdownCtx.Done() // Wait for the signal to shut down.

		// Create a context with timeout for the shutdown process itself.
		sctx, sStop := context.WithTimeout(context.Background(), s.HTTPShutdownTimeout)
		defer sStop()

		// Attempt graceful shutdown.
		//nolint:contextcheck // Shutdown timeout, if we inherit it will already be cancelled
		if err := httpServer.Shutdown(sctx); err != nil {
			// If graceful shutdown fails (e.g., timeout exceeded), force close.
			_ = httpServer.Close() // Error ignored on forced close.
		}
	}()

	return httpServer.ListenAndServe()
}

// Serve accepts incoming connections from the provided [net.Listener] and serves
// them using the Server's configured [Handler]. It blocks until the listener
// returns an error (e.g., due to closing) or the provided context is cancelled.
//
// For each accepted connection, it spawns a new goroutine running an [*RPCServer]
// instance configured with the Server's stream-based [NewDecoderFunc] and [NewEncoderFunc].
// The connection's [net.Conn] is added to the request context via the [CtxNetConn] key.
// If a [Binder] is configured on the Server, it is called for each new connection's RPCServer.
//
// The method ensures the listener is closed when the context is cancelled.
// It waits for all connection-handling goroutines to complete before returning.
//
// It is safe to call Serve multiple times concurrently with different listeners.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	var wg sync.WaitGroup
	defer wg.Wait() // Wait for all connection goroutines to finish before returning.

	// Create a cancellable context derived from the input context for managing the server loop.
	sctx, stop := context.WithCancel(ctx)
	defer stop() // Ensure stop is called when Serve exits.

	// Register a function to close the listener when the context is cancelled.
	context.AfterFunc(sctx, func() { _ = ln.Close() }) // Error ignored on close.

	// Loop indefinitely, accepting new connections.
	for {
		newConn, err := ln.Accept()
		if err != nil {
			return errors.Join(err, sctx.Err())
		}

		// Increment WaitGroup counter for the new connection goroutine.
		wg.Add(1)

		// Launch a goroutine to handle the new connection.
		go func(connCtx context.Context, server *Server, conn net.Conn) {
			defer wg.Done() // Decrement counter when goroutine finishes.

			// Create a new RPCServer instance for this connection.
			// Use the Server's configured decoder, encoder, and handler.
			rpcServer := NewStreamServer(server.NewDecoder(conn), server.NewEncoder(conn), server.handler)

			// Create a context specific to this connection, adding the net.Conn.
			// Use WithCancelCause for potential finer-grained shutdown reasons.
			cctx, connStop := context.WithCancelCause(context.WithValue(connCtx, CtxNetConn, conn))
			defer connStop(nil) // Ensure connection context is cancelled on exit.

			// Call the Binder if configured.
			if server.Binder != nil {
				server.Binder.Bind(cctx, rpcServer, connStop)
			}

			// Run the RPC server for this connection. This blocks until the connection
			// is closed or the context (cctx) is cancelled.
			_ = rpcServer.Run(cctx)
		}(sctx, s, newConn) // Pass the loop context (sctx) to the goroutine.
	}
}

// ServePacket reads packets from the provided [net.PacketConn] and processes them
// using the Server's configured [Handler]. It blocks until the connection
// returns an error or the provided context is cancelled.
//
// It spawns multiple goroutines (controlled by [Server.PacketRoutines]) to read
// and process packets concurrently. Each goroutine runs an [*RPCServer] instance
// configured with the Server's packet-based [NewPacketDecoderFunc] and [NewPacketEncoderFunc].
// The underlying [net.PacketConn] is added to the context via the [CtxNetPacketConn] key.
// The sender's address for each packet is typically added to the request context
// using the [CtxFromAddr] key.
//
// If a [Binder] is configured on the Server, it is called for each spawned RPCServer instance.
//
// The method ensures the PacketConn is closed when the context is cancelled.
// It waits for all processing goroutines to complete before returning, aggregating
// any errors encountered.
//
// It is safe to call ServePacket multiple times concurrently with different PacketConns.
func (s *Server) ServePacket(ctx context.Context, packetConn net.PacketConn) error {
	// Create a context with the PacketConn value and cancellation capability.
	sctx, stop := context.WithCancelCause(context.WithValue(ctx, CtxNetPacketConn, packetConn))
	defer stop(nil) // Ensure context cancellation propagates.

	errs := make(chan error, s.PacketRoutines) // Channel to collect errors from goroutines.

	var wg sync.WaitGroup

	for range s.PacketRoutines {
		wg.Add(1)

		go func(gctx context.Context, ch chan<- error) {
			defer wg.Done()
			defer stop(nil)

			// Create a new packet server
			rpcServer := NewPacketServer(s.NewPacketDecoder(packetConn), s.NewPacketEncoder(packetConn), s.handler)

			if s.Binder != nil {
				s.Binder.Bind(gctx, rpcServer, stop)
			}

			// And run it
			ch <- rpcServer.Run(gctx)
		}(sctx, errs)
	}

	wg.Wait()
	close(errs)

	var err error

	for e := range errs {
		err = errors.Join(err, e)
	}

	return errors.Join(err, context.Cause(sctx))
}
