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

// ContextKey is used as keys for context values.
type ContextKey int

const (
	// Key for the underlying net.Conn.
	CtxNetConn ContextKey = iota
	// Key for the underlying net.PacketConn.
	CtxNetPacketConn ContextKey = iota
	// Key for the underlying *http.Request.
	CtxHTTPRequest
	// Key for underlying from addr on packet servers.
	CtxFromAddr
	// Key for parent RPCServer.
	CtxRPCServer

	// Default header read timeout when listening on an http.Server.
	DefaultHTTPReadTimeout = 5
	// Default shutdown timeout when shutting down an http.Server.
	DefaultHTTPShutdownTimeout = 30
)

var ErrUnknownScheme = errors.New("unknown scheme in uri")

// Binder is used to configure a [*RPCServer] before it has started.
// It is called whenever a new [*RPCServer] is created.
//
// The cancel function may be used to stop the current [*RPCServer].
type Binder interface {
	// Called on new connections or new http requests
	Bind(context.Context, *RPCServer, context.CancelCauseFunc)
}

// Server allows for listening for new connections and serving them with the given handler.
// Each connection is run in its own [*RPCServer] and go-routine.
//
// For connection oriented servers, the context key of [CtxNetConn] is set with the accepted [net.Conn].
//
// For packet oriented servers, the context key of [CtxFromAddr] is set with the [net.Addr] from which the request was received.
type Server struct {
	handler          Handler
	Binder           Binder
	NewEncoder       NewEncoderFunc
	NewDecoder       NewDecoderFunc
	NewPacketEncoder NewPacketEncoderFunc
	NewPacketDecoder NewPacketDecoderFunc
	// Read header timeout if serving HTTP
	HTTPReadTimeout time.Duration
	// Graceful shutdown timeout if serving HTTP
	HTTPShutdownTimeout time.Duration
	// Number of go routines to service a packet listener. Defaults to min(runtime.NumCPU(), runtime.GOMAXPROCS())
	PacketRoutines int
}

// NewServer returns a new [*Server] that uses handler as the default [Handler].
func NewServer(handler Handler) *Server {
	var server Server

	server.handler = handler
	server.NewEncoder = NewEncoder
	server.NewDecoder = NewDecoder
	server.NewPacketEncoder = NewPacketEncoder
	server.NewPacketDecoder = NewPacketDecoder
	server.HTTPReadTimeout = time.Duration(DefaultHTTPReadTimeout) * time.Second
	server.HTTPShutdownTimeout = time.Duration(DefaultHTTPShutdownTimeout) * time.Second
	server.PacketRoutines = min(runtime.NumCPU(), runtime.GOMAXPROCS(-1))

	return &server
}

// ListenAndServer will listen on the given uri, serving it until the context is cancelled.
//
// Supported schemes: tcp, tcp4, tcp6, udp, udp4, udp6, unix, unixgram, unixpacket, http
//
// If listenURI is an http url, a [http.Sever] will be started to listen on the given address and serve the given path
//
// Examples uris: 'tcp:127.0.0.1:9090', 'udp::9090', 'http://127.0.0.1:8080/rpc', 'unix:///tmp/mysocket'
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

// listenAndServe listens on the given network and address, serving connections until its context is cancelled.
//
// It is safe to call listenAndServe multiple times with different listening address.
func (s *Server) listenAndServe(ctx context.Context, network, addr string) error {
	ln, err := net.Listen(network, addr)

	if err != nil {
		return err
	}

	return s.Serve(ctx, ln)
}

// listenAndServePacket listens on the given packet connection, serving requests until its context is cancelled.
//
// It is safe to call listenAndServePacket multiple times with different listening address.
func (s *Server) listenAndServePacket(ctx context.Context, network, addr string) error {
	pc, err := net.ListenPacket(network, addr)

	if err != nil {
		return err
	}

	return s.ServePacket(ctx, pc)
}

// listenAndServeHTTP starts an HTTP server on the given host:port of the uri and serves the given path with handler
//
// It is safe to call listenAndServeHTTP multiple times with different listening address.
func (s *Server) listenAndServeHTTP(ctx context.Context, uri *url.URL) error {
	shutdownCtx, stop := context.WithCancel(ctx)
	defer stop()

	httpMux := http.NewServeMux()

	handler := NewHTTPHandler(s.handler)
	handler.Binder = s.Binder
	handler.NewDecoder = s.NewDecoder
	handler.NewEncoder = s.NewEncoder

	httpMux.Handle(uri.Path, handler)

	httpServer := &http.Server{Addr: uri.Host, Handler: httpMux, ReadHeaderTimeout: s.HTTPReadTimeout}

	// Close listener on shutdown
	go func() {
		<-shutdownCtx.Done()

		sctx, sStop := context.WithTimeout(context.Background(), s.HTTPShutdownTimeout)
		defer sStop()

		//nolint:contextcheck // Shutdown timeout, if we inherit it will already be cancelled
		if err := httpServer.Shutdown(sctx); err != nil {
			// Forcefull
			httpServer.Close()
		}
	}()

	return httpServer.ListenAndServe()
}

// Serve listens on the given listener, serving connections until its context is cancelled.
//
// It is safe to call Serve multiple times with different listeners.
//
// The listener will be closed when the context is cancelled.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	var wg sync.WaitGroup
	defer wg.Wait()

	sctx, stop := context.WithCancel(ctx)
	defer stop()

	// Close listener on context cancel
	context.AfterFunc(sctx, func() { ln.Close() })

	/* Loop while accepting new connections */
	for {
		newConn, err := ln.Accept()
		if err != nil {
			return errors.Join(err, ctx.Err())
		}

		wg.Add(1)

		/* Spin up goroutine for the connection */
		go func(nctx context.Context, server *Server, conn net.Conn) {
			defer wg.Done()

			/* New Connection, create a backend for it */
			rpcServer := NewStreamServer(s.NewDecoder(conn), s.NewEncoder(conn), server.handler)

			cctx, nstop := context.WithCancelCause(context.WithValue(context.WithValue(nctx, CtxNetConn, conn), CtxRPCServer, rpcServer))
			defer nstop(nil)

			if s.Binder != nil {
				s.Binder.Bind(cctx, rpcServer, nstop)
			}

			/* And run */
			_ = rpcServer.Run(cctx)
		}(sctx, s, newConn)
	}
}

// ServePacket serves a listening [net.PacketConn], serving requests until its context is cancelled.
//
// It is safe to call ServePacket multiple times with different listeners.
//
// [Binder] is called for each [*RPCServer] spawned to serve the [net.PacketConn].
//
// The listener will be closed when the context is cancelled.
func (s *Server) ServePacket(ctx context.Context, packetConn net.PacketConn) error {
	sctx, stop := context.WithCancelCause(context.WithValue(ctx, CtxNetPacketConn, packetConn))
	defer stop(nil)

	errs := make(chan error, s.PacketRoutines)

	var wg sync.WaitGroup

	for range s.PacketRoutines {
		wg.Add(1)

		go func(gctx context.Context, ch chan<- error) {
			defer wg.Done()
			defer stop(nil)

			// Create a new packet server
			rpcServer := NewPacketServer(s.NewPacketDecoder(packetConn), s.NewPacketEncoder(packetConn), s.handler)

			// Add packet server to context
			pctx := context.WithValue(gctx, CtxRPCServer, rpcServer)

			if s.Binder != nil {
				s.Binder.Bind(pctx, rpcServer, stop)
			}

			// And run it
			ch <- rpcServer.Run(pctx)
		}(sctx, errs)
	}

	wg.Wait()
	close(errs)

	var err error

	for e := range errs {
		err = errors.Join(err, e)
	}

	return err
}
