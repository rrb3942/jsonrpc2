package jsonrpc2

// Response represents a jsonrpc2 server response.
//
// The spec requires that a response only have a [Result] OR an [Error], not both. Manually modifying or creating response
// structs may break this spec. It is recommended to use the constructor methods to avoid this.
//
//nolint:govet //We want order to match spec examples, even if not required
type Response struct {
	Jsonrpc Version `json:"jsonrpc"`
	Result  Result  `json:"result,omitempty,omitzero"`
	Error   Error   `json:"error,omitempty,omitzero"`
	ID      ID      `json:"id"` // ID must always be present in a response, even if null
}

// NewResponseWithResult returns a new response with the [ID] set to id and a [Result] set to r.
func NewResponseWithResult[I int64 | string](id I, r any) *Response {
	return &Response{ID: NewID(id), Result: NewResult(r)}
}

// NewResponseWithError returns a new response with the [ID] set to id and a [Error] set to e.
//
// If e is of type [Error] it will be used directly.
//
// Other values for error will automatically be converted to an [ErrInternalError] with the data field populated with the error string.
func NewResponseWithError[I int64 | string](id I, e error) *Response {
	return &Response{ID: NewID(id), Error: asError(e)}
}

// NewResponseError returns a new response with a null ID and [Error] set to e. It's primary use is for responding to malformed requests when the
// id could not be determined. [NewResponseWithResult] or [NewResponseWithError] should generally be more preferred.
//
// If e is of type [Error] it will be used directly.
//
// Other values for error will automatically be converted to an [ErrInternalError] with the data field populated with the error string.
func NewResponseError(e error) *Response {
	return &Response{ID: NewNullID(), Error: asError(e)}
}

// IsError returns true if the response contains an [Error].
func (r *Response) IsError() bool {
	return !r.Error.IsZero()
}

// Used in batch for identifying elements.
func (r *Response) id() ID {
	return r.ID
}
