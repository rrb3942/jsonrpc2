// Package jsonrpc2 provides an implementation of the [jsonrpc2 protocol]. It supports generic streaming connections, as well as serving packet connections.
//
// jsonrpc2 was designed to make it easy to plug in other compatible transports and json implementations.
//
// This library implements a strict client-server architecture and does not currently support bi-directional communication.
//
// This library is not compatible with jsonrpc 1.0.
//
// [jsonrpc2 protocol]: https://www.jsonrpc.org/specification
package jsonrpc2

import (
	"encoding/json"
)

var nullValue = json.RawMessage("null")

// Marshal is the Marshal function that will be used internally by various data types.
// This may be set by the application to implement a custom marshaling.
// See [Encoder] on requirements for any compatabil replacement function.
var Marshal = json.Marshal

// Unmarshal is the Unmarshal function that will be used internally by various data types.
// This may be set by the application to implement a custom unmarshalling.
// See [Decoder] on requirements for any compatible replacement function.
var Unmarshal = json.Unmarshal
