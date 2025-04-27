# jsonrpc2

[![Go Reference](https://pkg.go.dev/badge/github.com/rrb3942/jsonrpc2.svg)](https://pkg.go.dev/github.com/rrb3942/jsonrpc2)

`jsonrpc2` provides a robust and extensible Go implementation of the [JSON-RPC 2.0 protocol specification](https://www.jsonrpc.org/specification).

## Overview

This library facilitates communication between clients and servers using JSON-RPC 2.0. It adheres strictly to the specification and is **not compatible with JSON-RPC 1.0**. The core design focuses on a clear client-server architecture. Bi-directional communication (server initiating requests to the client) is not currently supported.

## Features

*   **Transport Agnostic**: Supports various underlying transports:
    *   Stream-based connections (`net.Conn` like TCP, Unix sockets) via `Server.Serve` and `Client`.
    *   Packet-based connections (`net.PacketConn` like UDP, Unix datagram) via `Server.ServePacket` and `Client`.
    *   HTTP(S) via `HTTPHandler` for servers and `NewHTTPBridge` for clients.
*   **Extensible**: Easily integrate custom logic:
    *   **Pluggable JSON Libraries**: Replace the standard `encoding/json` by overriding package-level variables (`Marshal`, `Unmarshal`, `NewJSONEncoder`, `NewJSONDecoder`). Use libraries like `github.com/bytedance/sonic` or others.
    *   **Custom Encoders/Decoders**: Implement the `Encoder`, `Decoder`, `PacketEncoder`, or `PacketDecoder` interfaces for fine-grained control over message processing and transport interaction.
    *   **Middleware/Hooks**: Use `Binder` for server lifecycle management and `Callbacks` for event handling (errors, panics).
    *   **Method Routing**: Use `MethodMux` to route incoming requests to specific handler functions based on the method name.
*   **Concurrency Control**: Configure server-side concurrency using `RPCServer` options (`NoRoutines`, `SerialBatch`).
*   **Context Propagation**: Leverages Go's `context` package for cancellation and passing request-scoped values (`CtxHTTPRequest`, `CtxNetConn`, etc.).
*   **Client Pooling**: Built-in connection pooling (`ClientPool`) for managing client connections efficiently.
*   **Simplified Client**: `Client` provides a straightforward interface for simple request/response scenarios.

## Basic Usage

See the [Go package documentation](https://pkg.go.dev/github.com/rrb3942/jsonrpc2) for detailed API information.

### Server (TCP Example)

```go
package main

import (
	"context"
	"errors"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/rrb3942/jsonrpc2"
)

// Simple echo handler function
func echoHandler(ctx context.Context, req *jsonrpc2.Request) (any, error) {
	// Access parameters via req.Params.Unmarshal(&yourStruct) or req.Params.RawMessage()
	log.Printf("Received echo request with params: %s", string(req.Params.RawMessage()))
	return req.Params.RawMessage(), nil // Echo back the parameters
}

func main() {
	mux := jsonrpc2.NewMethodMux()
	mux.RegisterFunc("echo", echoHandler) // Register the "echo" method

	server := jsonrpc2.NewServer(mux)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Println("JSON-RPC server listening on :9090")

	// Run the server until context is cancelled
	// ListenAndServe handles listener creation and shutdown.
	if err := server.ListenAndServe(ctx, "tcp::9090"); err != nil && !errors.Is(err, net.ErrClosed) {
		log.Printf("Server error: %v", err)
	}
	log.Println("Server shut down gracefully")
}

```

### Client (TCP Example)

```go
package main

import (
	"context"
	"log"
	"time"

	"github.com/rrb3942/jsonrpc2"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Dial establishes the connection and creates a basic client.
	// It uses a ClientPool internally.
	client, err := jsonrpc2.Dial(ctx, "tcp:localhost:9090")
	if err != nil {
		log.Fatalf("Failed to dial server: %v", err)
	}
	defer client.Close() // Important to close the client connection pool

	// Optional: Set a default timeout for all calls via this client instance.
	client.SetDefaultTimeout(5 * time.Second)

	params := map[string]string{"message": "hello world"}
	var result map[string]string // Variable to hold the result

	// Call the "echo" method on the server
	// BasicClient handles request ID generation.
	// Pass params directly (must be marshalable to JSON object/array or nil).
	response, err := client.Call(ctx, "echo", params)
	if err != nil {
		// This error could be a connection error, timeout, etc.
		log.Fatalf("RPC transport/call failed: %v", err)
	}

	// Check if the response itself indicates a JSON-RPC error
	if response.IsError() {
		log.Printf("Server returned JSON-RPC error: Code=%d, Message=%s, Data=%v",
			response.Error.Code, response.Error.Message, response.Error.Data.RawMessage())
		return
	}

	// Process the successful result
	// Use response.Result.Unmarshal(&yourStruct) or response.Result.RawMessage()
	if err := response.Result.Unmarshal(&result); err != nil {
		log.Fatalf("Failed to unmarshal result: %v", err)
	}
	log.Printf("Server responded successfully: %v", result)
}
```

## License

MIT

***Copyright (c) 2025 Ryan Bullock***
