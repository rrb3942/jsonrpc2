package jsonrpc2

import (
	"unsafe"
)

// Request represents a jsonrpc2 client request.
//
//nolint:govet //We want order to match spec examples, even if not required
type Request struct {
	Jsonrpc Version `json:"jsonrpc"`
	Method  string  `json:"method"`
	Params  Params  `json:"params,omitzero,omitempty"`
	ID      ID      `json:"id,omitzero"`
}

// NewRequest builds a new request for method with the given id.
func NewRequest[I int64 | string](id I, method string) *Request {
	return &Request{Method: method, ID: NewID(id)}
}

// NewRequest builds a new request for method with the given id, and the params set to p.
func NewRequestWithParams[I int64 | string](id I, method string, p Params) *Request {
	return &Request{Method: method, ID: NewID(id), Params: p}
}

// ResponseWithError constructs a response for the current [*Request] and populates the [Error] field.
// If e is of type Error it will be used directly.
// Other values for error will automatically be converted to an ErrInternalError with the data field populated with the error string.
func (r *Request) ResponseWithError(e error) *Response {
	return &Response{ID: r.ID, Error: asError(e)}
}

// ResponseWithResult constructs a response for the current [*Request] and populates the [Result] with result.
func (r *Request) ResponseWithResult(result any) *Response {
	return &Response{ID: r.ID, Result: NewResult(result)}
}

// IsNotification returns true if this request was a notification (no id field present).
func (r *Request) IsNotification() bool {
	return r.ID.IsZero()
}

// AsNotification returns the [Request] as a [*Notification].
//
// If the request is not a notification, nil will be returned.
//
// This is NOT a copy, but a pointer of type [*Notification] that points to the same
// memory as the request.
func (r *Request) AsNotification() *Notification {
	if r == nil || !r.IsNotification() {
		return nil
	}

	return (*Notification)(unsafe.Pointer(r))
}
