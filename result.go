package jsonrpc2

// Result represents the "result" field within a successful JSON-RPC 2.0 [Response] object.
// It acts as a container for the data returned by the invoked method.
//
// Result is an alias for the [Data] type. When a successful [Response] is unmarshaled,
// the "result" field's JSON value is stored internally as a [json.RawMessage]
// within this container. Use the [Result.Unmarshal] (inherited from [Data])
// method to decode the contained data into a specific Go type.
//
// See: https://www.jsonrpc.org/specification#response_object
type Result = Data

// NewResult creates a new [Result] instance containing the provided value `v`.
// The value `v` should be marshalable to JSON.
//
// This constructor is typically used indirectly via [NewResponseWithResult].
//
// Example:
//
//	// Create a result containing a simple string.
//	resultData := jsonrpc2.NewResult("operation successful")
//
// See [Data] for more details on how the value is handled.
func NewResult(v any) Result {
	// Ensure the 'present' flag is set, indicating this result exists,
	// even if 'v' itself is nil (which would represent JSON null).
	return Result{present: true, value: v}
}
