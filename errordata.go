package jsonrpc2

// ErrorData represents the optional "data" field within a JSON-RPC 2.0 [Error] object.
// It acts as a container for arbitrary, application-defined error information.
//
// ErrorData is an alias for the [Data] type. When an [Error] is unmarshaled,
// the "data" field's JSON value is stored internally as a [json.RawMessage]
// within this container. Use the [ErrorData.Unmarshal] (inherited from [Data])
// method to decode the contained data into a specific Go type.
//
// See: https://www.jsonrpc.org/specification#error_object
type ErrorData = Data

// NewErrorData creates a new [ErrorData] instance containing the provided value `v`.
// The value `v` should be marshalable to JSON.
//
// This constructor is typically used indirectly via [Error.WithData] or [NewErrorWithData].
//
// Example:
//
//	// Create error data containing details about an invalid field.
//	details := map[string]string{"field": "email", "reason": "must be a valid email address"}
//	errData := jsonrpc2.NewErrorData(details)
//
// See [Data] for more details on how the value is handled.
func NewErrorData(v any) ErrorData {
	// Ensure the 'present' flag is set, indicating this data exists,
	// even if 'v' itself is nil (which would represent JSON null).
	return ErrorData{present: true, value: v}
}
