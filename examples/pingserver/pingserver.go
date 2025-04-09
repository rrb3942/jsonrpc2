package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"strings"
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

	proto := flag.String("proto", "tcp", "Protocol to listen on (tcp, udp, unix, http, etc)")
	addr := flag.String("addr", "127.0.0.1:9090", "Network address to listen on in form of IP:PORT")
	flag.Parse()

	myHandler := &handler{}

	server := jsonrpc2.NewServer(myHandler)

	if strings.HasPrefix(*proto, "udp") || *proto == "unixgram" || *proto == "unixpacket" {
		if err := server.ListenAndServePacket(shutdownCtx, *proto, *addr); err != nil {
			log.Println(err)
		}

		return
	}

	if err := server.ListenAndServe(shutdownCtx, *proto, *addr); err != nil {
		log.Println(err)
	}
}
