package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/rrb3942/jsonrpc2"
)

func run() int {
	shutdownCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	method := flag.String("method", "ping", "Method to send")
	destURI := flag.String("dest", "tcp:127.0.0.1:9090", "Network address of the server in form of PROTO:IP:PORT. Maybe also be an HTTP URL.")
	flag.Parse()

	client, err := jsonrpc2.DialBasic(shutdownCtx, *destURI)

	if err != nil {
		log.Println(err)
		return -1
	}

	defer client.Close()

	resp, err := client.Call(shutdownCtx, *method, jsonrpc2.Params{})

	if err != nil {
		log.Println(err)
		return -2
	}

	if resp.IsError() {
		log.Println(resp.Error.Message)
		return -3
	}

	var reply any

	if err := resp.Result.Unmarshal(&reply); err != nil {
		log.Println(err)
		return -4
	}

	log.Printf("Got: %v!", reply)

	return 0
}

func main() {
	os.Exit(run())
}
