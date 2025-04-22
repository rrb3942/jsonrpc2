package jsonrpc2

// ErrorData represents the data field of a jsonrpc2 error object.
type ErrorData = Data

// NewErrorData returns a new ErrorData with is value set to v.
//
// See [Data] for how values are handled.
func NewErrorData(v any) ErrorData {
	return ErrorData{present: true, value: v}
}
