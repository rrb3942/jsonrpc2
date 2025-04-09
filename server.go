package jsonrpc2

import (
	"context"
	"errors"
	"net"
	"sync"
)

// ContextKey is used as keys for context values.
type ContextKey int

const (
	// Key for the underlying net.Conn.
	CtxNetConn ContextKey = iota
	// Key for the underlying *http.Request.
	CtxHTTPRequest ContextKey = iota
	// Key for underlying from addr on packet servers.
	CtxFromAddr ContextKey = iota
	// Key for parent StreamServer.
	CtxStreamServer ContextKey = iota
	// Key for parent PacketServer.
	CtxPacketServer ContextKey = iota
)

// The cancel function may be used to stop the current [*StreamServer].
type Binder interface {
	// Called on new connections or new http requests
	Bind(context.Context, *StreamServer, context.CancelCauseFunc)
}

// Server allows for listening for new connections and serving them with the given handler.
// Each connection is run in its own [*StreamServer] and go-routine.
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
}

// NewServer returns a new [*Server] that uses handler as the default [Handler].
func NewServer(handler Handler) *Server {
	var server Server

	server.handler = handler
	server.NewEncoder = NewEncoder
	server.NewDecoder = NewDecoder
	server.NewPacketEncoder = NewPacketEncoder
	server.NewPacketDecoder = NewPacketDecoder

	return &server
}

// ListenAndServe listens on the given network and address, serving connections until its context is cancelled.
//
// It is safe to call ListenAndServe multiple times with different listening address.
func (s *Server) ListenAndServe(ctx context.Context, network, addr string) error {
	ln, err := net.Listen(network, addr)

	if err != nil {
		return err
	}

	return s.Serve(ctx, ln)
}

// ListenAndServePacket listens on the given packet connection, serving requests until its context is cancelled.
//
// [Binder] is not called at all for packet connections.
//
// It is safe to call ListenAndServePacket multiple times with different listening address.
func (s *Server) ListenAndServePacket(ctx context.Context, network, addr string) error {
	pc, err := net.ListenPacket(network, addr)

	if err != nil {
		return err
	}

	return s.ServePacket(ctx, pc)
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

			cctx, nstop := context.WithCancelCause(context.WithValue(nctx, CtxNetConn, conn))
			defer nstop(nil)

			/* New Connection, create a backend for it */
			rpcServer := NewStreamServer(s.NewDecoder(conn), s.NewEncoder(conn), server.handler)

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
// [Binder] is not called at all for packet connections.
//
// The listener will be closed when the context is cancelled.
func (s *Server) ServePacket(ctx context.Context, pc net.PacketConn) error {
	// Create a new packet server
	rpcServer := NewPacketServer(s.NewPacketDecoder(pc), s.NewPacketEncoder(pc), s.handler)
	// And run it
	return rpcServer.Run(ctx)
}
