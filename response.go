package jsonrpc2

import "errors"

var (
	// ErrResponseIsError is returned when trying to use an error response as a regular response.
	ErrResponseIsError = errors.New("response is an error")

	// ErrResponseNotError is returned when trying to use an error response as a regular response.
	ErrResponseNotError = errors.New("response is a response")
)

// Response represents a JSON-RPC 2.0 response object.
//
// A response must contain either a [Result] or an [Error], but not both.
// The [ID] field must mirror the ID from the request it is responding to.
// Use the constructor functions ([NewResponseWithResult], [NewResponseWithError], [NewResponseError])
// to ensure compliance with the specification.
//
// See: https://www.jsonrpc.org/specification#response_object
//
//nolint:govet // We want order to match spec examples, even if not required.
type Response struct {
	Jsonrpc Version `json:"jsonrpc"`                   // Specifies the JSON-RPC version ("2.0").
	Result  Result  `json:"result,omitempty,omitzero"` // The result of the method invocation, if successful. Omitted if an error occurred.
	Error   Error   `json:"error,omitempty,omitzero"`  // An error object if an error occurred during invocation. Omitted if successful.
	ID      ID      `json:"id"`                        // Must be the same as the request ID. Should be null if the request ID could not be determined (e.g., parse error).
}

// NewResponseWithResult creates a successful response for a given request ID and result.
// The result 'r' will be marshaled to JSON.
//
// Example:
//
//	// Responding to a request with ID 1 and result "pong"
//	resp := jsonrpc2.NewResponseWithResult(1, "pong")
//	// Marshals to: {"jsonrpc":"2.0","result":"pong","id":1}
func NewResponseWithResult[I int64 | string](id I, r any) *Response {
	return &Response{ID: NewID(id), Result: NewResult(r)}
}

// NewResponseWithError creates an error response for a given request ID and error.
//
// If 'e' is already a jsonrpc2.[Error], it is used directly.
// Otherwise, 'e' is wrapped in a standard [ErrInternalError], and its Error() string
// becomes the 'data' field of the JSON-RPC error object.
//
// Example:
//
//	// Responding to request ID "req-01" with a custom RPC error
//	rpcErr := jsonrpc2.NewError(100, "Resource not found")
//	resp1 := jsonrpc2.NewResponseWithError("req-01", rpcErr)
//	// Marshals to: {"jsonrpc":"2.0","error":{"code":100,"message":"Resource not found"},"id":"req-01"}
//
//	// Responding to request ID 2 with a standard Go error
//	stdErr := fmt.Errorf("database connection failed")
//	resp2 := jsonrpc2.NewResponseWithError(2, stdErr)
//	// Marshals to: {"jsonrpc":"2.0","error":{"code":-32603,"message":"Internal error","data":"database connection failed"},"id":2}
func NewResponseWithError[I int64 | string](id I, e error) *Response {
	return &Response{ID: NewID(id), Error: asError(e)}
}

// NewResponseError creates an error response with a null ID.
// This is primarily used when a request is malformed and its ID cannot be determined.
// For errors related to valid requests, use [NewResponseWithError].
//
// If 'e' is already a jsonrpc2.[Error], it is used directly.
// Otherwise, 'e' is wrapped in a standard [ErrInternalError], and its Error() string becomes the 'data' field.
//
// Example:
//
//	// Responding to a parsing error where the request ID is unknown
//	parseErr := jsonrpc2.ErrParseErrorError.WithData("Invalid JSON received")
//	resp := jsonrpc2.NewResponseError(parseErr)
//	// Marshals to: {"jsonrpc":"2.0","error":{"code":-32700,"message":"Parse error","data":"Invalid JSON received"},"id":null}
func NewResponseError(e error) *Response {
	// TODO: Consider smarter error code mapping here (e.g., check if e matches ErrParseErrorError, etc.)
	return &Response{ID: NewNullID(), Error: asError(e)}
}

// Unmarshal attempts to unmarshal the result of the JSON-RPC response into the
// provided variable v.
//
// If the response itself represents an error (i.e., r.Error is not zero),
// Unmarshal returns ErrResponseIsError and does not attempt to unmarshal the result.
// Otherwise, it calls the Unmarshal method on r.Result, passing v as the target.
//
// The v argument must be a pointer, similar to the behavior of json.Unmarshal.
func (r *Response) Unmarshal(v any) error {
	if r.Error.IsZero() {
		return r.Result.Unmarshal(v)
	}

	return ErrResponseIsError
}

// UnmarshalError unmarshals the error data from the response into the given
// variable v.
//
// If the response does not represent an error (i.e., r.Error is nil or zero),
// it returns ErrResponseIsResponse. Otherwise, it attempts to unmarshal
// the r.Error.Data field into v.
//
// It returns an error if unmarshalling fails or if the response is not an error.
func (r *Response) UnmarshalError(v any) error {
	if r.Error.IsZero() {
		return ErrResponseNotError
	}

	return r.Error.Data.Unmarshal(v)
}

// IsError returns true if the response contains an [Error] object (indicating failure),
// and false otherwise (indicating success).
func (r *Response) IsError() bool {
	return !r.Error.IsZero()
}

// id returns the response ID. Used internally, primarily for matching responses in batch requests.
func (r *Response) id() ID {
	return r.ID
}
