package jsonrpc2

// Result represents the result field of a jsonrpc2 response object.
type Result = Data

// NewResult returns a new Result with is value set to v.
//
// See [Data] for how values are handled.
func NewResult(v any) Result {
	return Result{present: true, value: v}
}
