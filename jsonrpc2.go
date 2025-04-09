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
