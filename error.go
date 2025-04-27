package jsonrpc2

import (
	"errors"
)

// Predefined JSON-RPC 2.0 errors as defined by the specification.
// See: https://www.jsonrpc.org/specification#error_object
var (
	ErrParseError       = NewError(-32700, "Parse error")       // Invalid JSON was received by the server. An error occurred on the server while parsing the JSON text.
	ErrInvalidRequest   = NewError(-32600, "Invalid Request")   // The JSON sent is not a valid Request object.
	ErrMethodNotFound   = NewError(-32601, "Method not found")  // The method does not exist / is not available.
	ErrInvalidParams    = NewError(-32602, "Invalid params")    // Invalid method parameter(s).
	ErrInternalError    = NewError(-32603, "Internal error")    // Internal JSON-RPC error.
	ErrServerOverloaded = NewError(-32000, "Server overloaded") // Reserved for implementation-defined server-errors (-32000 to -32099).
)

// Error represents a JSON-RPC 2.0 error object.
// It encapsulates a Code, Message, and optional Data.
//
// Error implements the standard Go `error` interface via its [Error] method,
// allowing it to be used like any other Go error. It also supports comparison
// using [errors.Is] based on the [Error.Code].
//
// Use the constructor functions [NewError] or [NewErrorWithData] to create instances.
// Access fields using the [Error.Code], [Error.Message], and [Error.Data] methods.
//
// See: https://www.jsonrpc.org/specification#error_object
//
//nolint:govet // We want order to match spec examples, even if not required.
type Error struct {
	present bool
	Data    ErrorData `json:"data,omitempty,omitzero"`
	Message string    `json:"message"`
	Code    int64     `json:"code"` // A Number that indicates the error type that occurred.
}

// NewError creates a new [Error] with the specified code and message.
//
// Example:
//
//	err := jsonrpc2.NewError(-32001, "Application specific error")
//	fmt.Println(err.Code, err.Message) // Output: -32001 Application specific error
func NewError(code int64, msg string) Error {
	return Error{present: true, Code: code, Message: msg}
}

// NewErrorWithData creates a new [Error] with the specified code, message, and additional data.
// The data field can contain any value that is serializable to JSON.
//
// Example:
//
//	details := map[string]string{"field": "username", "issue": "cannot be empty"}
//	err := jsonrpc2.NewErrorWithData(-32602, "Invalid params", details)
//	fmt.Println(err.Code, err.Message) // Output: -32602 Invalid params
//	// err.Data can be used to retrieve the details map after unmarshalling.
func NewErrorWithData(code int64, msg string, data any) Error {
	return Error{present: true, Code: code, Message: msg, Data: NewErrorData(data)}
}

// asError converts a standard Go error into a jsonrpc2 [Error].
// If the input error `e` can be type-asserted to an [Error] using `errors.As`,
// it is returned directly. Otherwise, it wraps the error's string representation
// within a standard [ErrInternalError]. This is primarily used internally when
// constructing error responses.
func asError(e error) Error {
	var je Error

	if errors.As(e, &je) {
		return je
	}

	// If it's not already a jsonrpc2.Error, wrap it.
	// TODO: Consider mapping common Go errors (e.g., context.DeadlineExceeded) to specific RPC errors.
	return ErrInternalError.WithData(e.Error())
}

// WithData returns a *new* [Error] instance based on the original,
// but with its data field replaced by the provided `data`.
// The original error remains unchanged.
//
// Example:
//
//	detailedErr := jsonrpc2.ErrInvalidRequest.WithData("Request missing 'method' field")
func (e Error) WithData(data any) Error {
	return NewErrorWithData(e.Code, e.Message, data)
}

// Is reports whether the target error `t` is considered equivalent to this [Error].
// Equivalence is determined by comparing the error codes. This allows using `errors.Is`
// with predefined errors like [ErrInvalidParams].
//
// Example:
//
//	err := jsonrpc2.NewErrorWithData(-32602, "Invalid params", "Missing required field 'x'")
//	if errors.Is(err, jsonrpc2.ErrInvalidParams) {
//	    fmt.Println("Error is an Invalid Parameters error.") // This will be printed
//	}
func (e Error) Is(t error) bool {
	if jerr, ok := t.(Error); ok {
		return (e.present && jerr.present && e.Code == jerr.Code)
	}

	if jerr, ok := t.(*Error); ok {
		return (e.present && jerr.present && e.Code == jerr.Code)
	}

	return false
}

// IsZero returns true if the error represents the zero value (i.e., it was not
// properly initialized or unmarshaled). An error created with [NewError] or
// [NewErrorWithData] will not be zero.
func (e *Error) IsZero() bool {
	return !e.present
}

// Error implements the standard Go `error` interface. It returns the message
// part of the JSON-RPC error.
func (e Error) Error() string {
	return e.Message
}

// UnmarshalJSON implements the [json.Unmarshaler] interface.
// It allows the [Error] type to be correctly populated from JSON data.
func (e *Error) UnmarshalJSON(b []byte) error {
	var rpcErr struct {
		Data    ErrorData `json:"data,omitempty,omitzero"`
		Message string    `json:"message"`
		Code    int64     `json:"code"` // A Number that indicates the error type that occurred.
	}

	if err := Unmarshal(b, &rpcErr); err != nil {
		return err
	}

	e.present = true
	e.Code = rpcErr.Code
	e.Message = rpcErr.Message
	e.Data = rpcErr.Data

	return nil
}
