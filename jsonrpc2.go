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
//   - Transport Agnostic: Supports various underlying transports:
//   - Stream-based connections ([net.Conn] like TCP, Unix sockets) via [Server.Serve] and [Client].
//   - Packet-based connections ([net.PacketConn] like UDP, Unix datagram) via [Server.ServePacket] and [Client].
//   - HTTP(S) via [HTTPHandler] for servers and [NewHTTPBridge] for clients.
//   - Extensible: Easily integrate custom logic:
//   - Pluggable JSON Libraries: Replace the standard `encoding/json` by overriding package-level variables ([Marshal], [Unmarshal], [NewJSONEncoder], [NewJSONDecoder]).
//   - Custom Encoders/Decoders: Implement the [Encoder], [Decoder], [PacketEncoder], or [PacketDecoder] interfaces for fine-grained control over message processing and transport interaction.
//   - Middleware/Hooks: Use [Binder] for server lifecycle management and [Callbacks] for event handling (errors, panics).
//   - Method Routing: Use [MethodMux] to route incoming requests to specific handler functions based on the method name.
//   - Concurrency Control: Configure server-side concurrency using [RPCServer] options ([NoRoutines], [SerialBatch]).
//   - Context Propagation: Leverages Go's `context` package for cancellation and passing request-scoped values ([CtxHTTPRequest], [CtxNetConn], etc.).
//
// # Basic Usage
//
// ## Server (TCP)
//
//	package main
//
//	import (
//		"context"
//		"log"
//		"net"
//		"os"
//		"os/signal"
//		"syscall"
//
//		"github.com/your_org/jsonrpc2" // Adjust import path
//	)
//
//	// Simple echo handler function
//	func echoHandler(ctx context.Context, req *jsonrpc2.Request) (any, error) {
//		// Access parameters via req.Params.Unmarshal(&yourStruct) or req.Params.RawMessage()
//		log.Printf("Received echo request with params: %s", string(req.Params.RawMessage()))
//		return req.Params, nil // Echo back the parameters
//	}
//
//	func main() {
//		mux := jsonrpc2.NewMethodMux()
//		mux.RegisterFunc("echo", echoHandler) // Register the "echo" method
//
//		server := jsonrpc2.NewServer(mux)
//
//		listener, err := net.Listen("tcp", ":9090")
//		if err != nil {
//			log.Fatalf("Failed to listen: %v", err)
//		}
//		defer listener.Close()
//
//		log.Println("JSON-RPC server listening on :9090")
//
//		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
//		defer stop()
//
//		// Run the server until context is cancelled
//		if err := server.Serve(ctx, listener); err != nil && !errors.Is(err, net.ErrClosed) {
//			log.Printf("Server error: %v", err)
//		}
//		log.Println("Server shut down gracefully")
//	}
//
// ## Client (TCP)
//
//	package main
//
//	import (
//		"context"
//		"log"
//		"time"
//
//		"github.com/your_org/jsonrpc2" // Adjust import path
//	)
//
//	func main() {
//		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
//		defer cancel()
//
//		// DialBasic establishes the connection and creates a basic client.
//		client, err := jsonrpc2.DialBasic(ctx, "tcp://localhost:9090")
//		if err != nil {
//			log.Fatalf("Failed to dial server: %v", err)
//		}
//		defer client.Close() // Important to close the client connection
//
//		params := map[string]string{"message": "hello world"}
//		var result map[string]string // Variable to hold the result
//
//		// Call the "echo" method on the server
//		err = client.Call(ctx, "echo", params, &result)
//		if err != nil {
//			log.Fatalf("RPC call failed: %v", err)
//		}
//
//		log.Printf("Server responded: %v", result)
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
// from a third-party JSON library like `github.com/json-iterator/go`.
//
// The replacement function must have the same signature as `json.Marshal`.
// Ensure the replacement is compatible with the JSON-RPC 2.0 specification
// and the expectations of the [Encoder] and [PacketEncoder] interfaces used
// within this package (e.g., handling of standard types, `json.Marshaler`).
//
// Example (using json-iterator):
//
//	import jsoniter "github.com/json-iterator/go"
//
//	func init() {
//	    jsonrpc2.Marshal = jsoniter.ConfigCompatibleWithStandardLibrary.Marshal
//	}
var Marshal = json.Marshal

// Unmarshal defines the function used for unmarshaling JSON []byte into Go types.
// By default, it uses [encoding/json.Unmarshal]. Applications can replace this
// variable *at startup* with a different unmarshaling function.
//
// The replacement function must have the same signature as `json.Unmarshal`.
// Ensure the replacement is compatible with the JSON-RPC 2.0 specification
// and the expectations of the [Decoder] and [PacketDecoder] interfaces (e.g.,
// handling of standard types, `json.Unmarshaler`, `json.RawMessage`).
//
// Example (using json-iterator):
//
//	import jsoniter "github.com/json-iterator/go"
//
//	func init() {
//	    jsonrpc2.Unmarshal = jsoniter.ConfigCompatibleWithStandardLibrary.Unmarshal
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
// Example (using json-iterator):
//
//	import (
//	    "io"
//	    jsoniter "github.com/json-iterator/go"
//	    "github.com/your_org/jsonrpc2" // Adjust import path
//	)
//
//	type jsonIterEncoder struct {
//	    enc *jsoniter.Encoder
//	}
//
//	func (j *jsonIterEncoder) Encode(v any) error {
//	    return j.enc.Encode(v)
//	}
//
//	func init() {
//	    jsonrpc2.NewJSONEncoder = func(w io.Writer) jsonrpc2.JSONEncoder {
//	        enc := jsoniter.ConfigCompatibleWithStandardLibrary.NewEncoder(w)
//	        // jsoniter doesn't add newlines by default, which json.Encoder does.
//	        // Add it if your application relies on newline separation.
//	        // enc.SetIndent("", "") // Ensure no extra indentation
//	        return &jsonIterEncoder{enc: enc} // Wrap if necessary or return directly if interface matches
//	    }
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
// Example (using json-iterator):
//
//	import (
//	    "io"
//	    jsoniter "github.com/json-iterator/go"
//	    "github.com/your_org/jsonrpc2" // Adjust import path
//	)
//
//	func init() {
//	    jsonrpc2.NewJSONDecoder = func(r io.Reader) jsonrpc2.JSONDecoder {
//	        // json.Decoder uses UseNumber() by default, jsoniter does not.
//	        // Configure jsoniter decoder as needed for compatibility.
//	        dec := jsoniter.ConfigCompatibleWithStandardLibrary.NewDecoder(r)
//	        // dec.UseNumber() // Uncomment if number handling needs to match json.Decoder
//	        return dec // jsoniter.Decoder already implements the interface
//	    }
//	}
var NewJSONDecoder = func(r io.Reader) JSONDecoder { return json.NewDecoder(r) }
