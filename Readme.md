# jsonrpc2 #
jsonrpc2 is an implementation of the [jsonrpc2 specification](https://www.jsonrpc.org/specification) for Go. It is designed to be easy to use and extensible.

jsonrpc2 does not support jsonrpc 1.0, and does not currently implement bi-directional RPC calls, instead focusing on the client-server model.

Please see the godoc for the API details.

## Features ##
* Server and Client implementations
* Stream and Packet servers
* Simple handler interface for serving requests
* net/http Handler implementation
* Binder interface for configuring servers on new client connections
* Batch and Notification support
* Full context propagation
* Can we used with any io.Reader and io.Writer
* Customizable json encoders and decoders
* Server callback support for notable events

# License #
MIT

***Copyright (c) 2025 Ryan Bullock***
