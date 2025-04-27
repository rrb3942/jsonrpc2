// Package jsonrpc2 provides a robust and extensible implementation of the JSON-RPC 2.0 protocol specification.
//
// # Overview
//
// This library facilitates communication between clients and servers using JSON-RPC 2.0.
// It adheres strictly to the specification and is not compatible with JSON-RPC 1.0.
// The core design focuses on a clear client-server architecture. Bi-directional
// communication (server initiating requests to the client) is not currently supported.
//
// # Features
//
//   - Transport agnostic, supports various underlying transports.
//   - Stream-based connections ([net.Conn] like TCP, Unix sockets) via [Server.Serve] and [Client].
//   - Packet-based connections ([net.PacketConn] like UDP, Unix datagram) via [Server.ServePacket] and [Client].
//   - HTTP(S) via [HTTPHandler] for servers and [NewHTTPBridge] for clients.
//   - Pluggable JSON Libraries: Replace the standard `encoding/json` by overriding package-level variables ([Marshal], [Unmarshal], [NewJSONEncoder], [NewJSONDecoder]).
//   - Custom Encoders/Decoders: Implement the [Encoder], [Decoder], [PacketEncoder], or [PacketDecoder] interfaces for fine-grained control over message processing and transport interaction.
//   - Middleware/Hooks: Use [Binder] for server lifecycle management and [Callbacks] for event handling (errors, panics).
//   - Method Routing: Use [MethodMux] to route incoming requests to specific handler functions based on the method name.
//   - Concurrency Control: Configure server-side concurrency using [RPCServer] options ([NoRoutines], [SerialBatch]).
//   - Context Propagation: Leverages Go's `context` package for cancellation and passing request-scoped values ([CtxHTTPRequest], [CtxNetConn], etc.).
//
// # Basic Usage
//
// # Server (TCP Example)
//
//	package main
//
//	import (
//		"context"
//		"errors"
//		"log"
//		"net"
//		"os"
//		"os/signal"
//		"syscall"
//
//		"github.com/rrb3942/jsonrpc2"
//	)
//
//	// Simple echo handler function
//	func echoHandler(ctx context.Context, req *jsonrpc2.Request) (any, error) {
//		// Access parameters via req.Params.Unmarshal(&yourStruct) or req.Params.RawMessage()
//		log.Printf("Received echo request with params: %s", string(req.Params.RawMessage()))
//		return req.Params.RawMessage(), nil // Echo back the parameters
//	}
//
//	func main() {
//		mux := jsonrpc2.NewMethodMux()
//		mux.RegisterFunc("echo", echoHandler) // Register the "echo" method
//
//		server := jsonrpc2.NewServer(mux)
//
//		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
//		defer stop()
//
//		log.Println("JSON-RPC server listening on :9090")
//
//		// Run the server until context is cancelled
//		// ListenAndServe handles listener creation and shutdown.
//		if err := server.ListenAndServe(ctx, "tcp::9090"); err != nil && !errors.Is(err, net.ErrClosed) {
//			log.Printf("Server error: %v", err)
//		}
//		log.Println("Server shut down gracefully")
//	}
//
// # Client (TCP Example)
//
//	package main
//
//	import (
//		"context"
//		"log"
//		"time"
//
//		"github.com/rrb3942/jsonrpc2"
//	)
//
//	func main() {
//		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
//		defer cancel()
//
//		// Dial establishes the connection and creates a client.
//		// It uses a ClientPool internally.
//		client, err := jsonrpc2.Dial(ctx, "tcp:localhost:9090")
//		if err != nil {
//			log.Fatalf("Failed to dial server: %v", err)
//		}
//		defer client.Close() // Important to close the client connection pool
//
//		// Optional: Set a default timeout for all calls via this client instance.
//		client.SetDefaultTimeout(5 * time.Second)
//
//		params := map[string]string{"message": "hello world"}
//		var result map[string]string // Variable to hold the result
//
//		// Call the "echo" method on the server
//		// BasicClient handles request ID generation.
//		// Pass params directly (must be marshalable to JSON object/array or nil).
//		response, err := client.Call(ctx, "echo", params)
//		if err != nil {
//			// This error could be a connection error, timeout, etc.
//			log.Fatalf("RPC transport/call failed: %v", err)
//		}
//
//		// Check if the response itself indicates a JSON-RPC error
//		if response.IsError() {
//			log.Printf("Server returned JSON-RPC error: Code=%d, Message=%s, Data=%v",
//				response.Error.Code, response.Error.Message, response.Error.Data.RawMessage())
//			return
//		}
//
//		// Process the successful result
//		// Use response.Result.Unmarshal(&yourStruct) or response.Result.RawMessage()
//		if err := response.Result.Unmarshal(&result); err != nil {
//			log.Fatalf("Failed to unmarshal result: %v", err)
//		}
//		log.Printf("Server responded successfully: %v", result)
//	}
//
// [jsonrpc2 protocol]: https://www.jsonrpc.org/specification
package jsonrpc2

import (
	"encoding/json"
	"io"
)

var nullValue = json.RawMessage("null") // Represents the JSON `null` value.

// Marshal defines the function used for marshaling Go types into JSON []byte.
// By default, it uses [encoding/json.Marshal]. Applications can replace this
// variable *at startup* with a different marshaling function, for example,
// from a third-party JSON library like `github.com/bytedance/sonic`.
//
// The replacement function must have the same signature as `json.Marshal`.
// Ensure the replacement is compatible with the JSON-RPC 2.0 specification
// and the expectations of the [Encoder] and [PacketEncoder] interfaces used
// within this package (e.g., handling of standard types, `json.Marshaler`).
//
// Example (using sonic):
//
//	import "github.com/bytedance/sonic"
//
//	func init() {
//	    jsonrpc2.Marshal = sonic.ConfigDefault.Marshal
//	}
var Marshal = json.Marshal

// Unmarshal defines the function used for unmarshalling JSON []byte into Go types.
// By default, it uses [encoding/json.Unmarshal]. Applications can replace this
// variable *at startup* with a different unmarshalling function.
//
// The replacement function must have the same signature as `json.Unmarshal`.
// Ensure the replacement is compatible with the JSON-RPC 2.0 specification
// and the expectations of the [Decoder] and [PacketDecoder] interfaces (e.g.,
// handling of standard types, `json.Unmarshaler`, `json.RawMessage`).
//
// Example (using sonic):
//
//	import "github.com/bytedance/sonic"
//
//	func init() {
//	    jsonrpc2.Unmarshal = sonic.ConfigDefault.Unmarshal
//	}
var Unmarshal = json.Unmarshal

// JSONEncoder defines the interface required for stream-based JSON encoding,
// compatible with [encoding/json.Encoder]. It's used by the default implementations
// of [Encoder] and [PacketEncoder].
type JSONEncoder interface {
	// Encode writes the JSON encoding of v to the stream, followed by a newline character.
	Encode(v any) error
}

// NewJSONEncoder defines the function used to create new [JSONEncoder] instances.
// By default, it returns a standard [encoding/json.Encoder]. Applications can
// replace this variable *at startup* to use a custom encoder implementation,
// perhaps one that wraps a different JSON library or adds custom behavior.
//
// The returned [JSONEncoder] must be compatible with the usage within the
// default [Encoder] and [PacketEncoder] implementations.
//
// Example (using sonic):
//
//	import (
//	    "io"
//	    "github.com/bytedance/sonic"
//	    "github.com/rrb3942/jsonrpc2"
//	)
//
//	func init() {
//	    jsonrpc2.NewJSONEncoder = func(w io.Writer) jsonrpc2.JSONEncoder { return sonic.ConfigDefault.NewEncoder(w) }
//	}
var NewJSONEncoder = func(w io.Writer) JSONEncoder { return json.NewEncoder(w) }

// JSONDecoder defines the interface required for stream-based JSON decoding,
// compatible with [encoding/json.Decoder]. It's used by the default implementations
// of [Decoder] and [PacketDecoder].
type JSONDecoder interface {
	// Decode reads the next JSON-encoded value from its input and stores it in the value pointed to by v.
	Decode(v any) error
}

// NewJSONDecoder defines the function used to create new [JSONDecoder] instances.
// By default, it returns a standard [encoding/json.Decoder]. Applications can
// replace this variable *at startup* to use a custom decoder implementation.
//
// The returned [JSONDecoder] must be compatible with the usage within the
// default [Decoder] and [PacketDecoder] implementations. Ensure it handles
// JSON-RPC structures correctly (e.g., reading distinct objects/arrays).
//
// Example (using sonic):
//
//	import (
//	    "io"
//	    "github.com/bytedance/sonic"
//	    "github.com/rrb3942/jsonrpc2"
//	)
//
//	func init() {
//	    jsonrpc2.NewJSONDecoder = func(r io.Reader) jsonrpc2.JSONDecoder { return sonic.ConfigDefault.NewDecoder(r) }
//	}
var NewJSONDecoder = func(r io.Reader) JSONDecoder { return json.NewDecoder(r) }
