package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"

	"github.com/rrb3942/jsonrpc2"
)

type handler struct{}

func (h *handler) Handle(ctx context.Context, req *jsonrpc2.Request) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if req.Method == "ping" {
		return "pong", nil
	}

	return nil, jsonrpc2.ErrMethodNotFound
}

func main() {
	shutdownCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	listenURI := flag.String("listen", "tcp:127.0.0.1:9090", "Network address to listen on in form of PROTO:IP:PORT. May also be an HTTP url and path to listen and serve on.")
	flag.Parse()

	myHandler := &handler{}

	server := jsonrpc2.NewServer(myHandler)

	if err := server.ListenAndServe(shutdownCtx, *listenURI); err != nil {
		log.Println(err)
	}
}
