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

// Error represents a JSON-RPC 2.0 error object as defined in the specification.
// It encapsulates a numeric code, a descriptive message, and optional,
// application-defined data.
//
// This type implements the standard Go `error` interface via its [Error.Error] method,
// allowing it to be used in standard Go error handling patterns. It also supports
// comparison using [errors.Is] by comparing error codes (see [Error.Is]).
//
// Use the constructor functions [NewError] or [NewErrorWithData] to create valid
// instances. Access the error details using the [Error.Code], [Error.Message],
// and [Error.Data] fields (note: Data is of type [ErrorData]). Check if an
// Error instance is uninitialized using [Error.IsZero].
//
// See: https://www.jsonrpc.org/specification#error_object
//
//nolint:govet // Field order matches spec examples for clarity, not strict requirement.
type Error struct {
	present bool      // Internal flag to distinguish zero value from explicitly set error.
	Code    int64     `json:"code"`    // A number indicating the error type. Standard codes are predefined.
	Message string    `json:"message"` // A string providing a short description of the error.
	Data    ErrorData `json:"data,omitempty,omitzero"` // Optional additional information about the error.
}

// NewError creates a new [Error] instance with the specified code and message.
// The Data field will be absent.
//
// Example:
//
//	appErr := jsonrpc2.NewError(-32001, "Application specific error")
//	fmt.Printf("Code: %d, Message: %s\n", appErr.Code, appErr.Message)
//	// Output: Code: -32001, Message: Application specific error
func NewError(code int64, msg string) Error {
	return Error{present: true, Code: code, Message: msg}
}

// NewErrorWithData creates a new [Error] instance with the specified code, message,
// and additional data. The provided `data` value will be wrapped in an [ErrorData] struct.
// The `data` should be marshalable to JSON.
//
// Example:
//
//	details := map[string]any{"field": "username", "issue": "cannot be empty", "attempt": 3}
//	paramErr := jsonrpc2.NewErrorWithData(-32602, "Invalid params", details)
//
//	// To access the data later (after receiving an error response):
//	var receivedDetails map[string]any
//	if err := paramErr.Data.Unmarshal(&receivedDetails); err == nil {
//	    fmt.Printf("Issue with field '%s': %s (attempt %v)\n",
//	        receivedDetails["field"], receivedDetails["issue"], receivedDetails["attempt"])
//	}
//	// Output: Issue with field 'username': cannot be empty (attempt 3)
func NewErrorWithData(code int64, msg string, data any) Error {
	return Error{present: true, Code: code, Message: msg, Data: NewErrorData(data)}
}

// asError converts a standard Go error into a jsonrpc2 [Error] suitable for responses.
// If the input error `e` can be unwrapped to a jsonrpc2 [Error] using [errors.As],
// that specific [Error] is returned. Otherwise, it wraps the original error's message
// within a standard [ErrInternalError], using the error message as the `data` field.
// This function is primarily used internally by the server implementation.
func asError(e error) Error {
	var je Error

	if errors.As(e, &je) {
		return je
	}

	// If it's not already a jsonrpc2.Error, wrap it.
	// TODO: Consider mapping common Go errors (e.g., context.DeadlineExceeded) to specific RPC errors.
	return ErrInternalError.WithData(e.Error())
}

// WithData creates and returns a *new* [Error] instance based on the receiver `e`,
// but with its data field replaced by the provided `data` value (wrapped in [ErrorData]).
// The original error `e` remains unchanged. This is useful for adding context to
// predefined errors.
//
// Example:
//
//	// Add specific details to a standard error
//	detailedErr := jsonrpc2.ErrInvalidRequest.WithData("Request must include 'id' field for non-notifications")
//	fmt.Println(detailedErr.Code)    // Output: -32600
//	fmt.Println(detailedErr.Message) // Output: Invalid Request
//	// detailedErr.Data now contains "Request must include 'id' field for non-notifications"
func (e Error) WithData(data any) Error {
	// Create a new Error, preserving Code and Message, but setting new Data.
	return NewErrorWithData(e.Code, e.Message, data)
}

// Is reports whether the target error `t` is considered equivalent to this [Error] `e`.
// Equivalence is determined solely by comparing the [Error.Code] fields of both errors.
// This allows using [errors.Is] to check if an error matches one of the predefined
// error types (like [ErrInvalidParams], [ErrMethodNotFound], etc.) regardless of
// the message or data content.
//
// Example:
//
//	func handleRequest(params any) error {
//	    if params == nil {
//	        // Return a specific instance of InvalidParams with details
//	        return jsonrpc2.ErrInvalidParams.WithData("Parameters cannot be null")
//	    }
//	    // ... process params ...
//	    return nil
//	}
//
//	err := handleRequest(nil)
//	if errors.Is(err, jsonrpc2.ErrInvalidParams) {
//	    // This block will execute because the codes match (-32602)
//	    fmt.Println("Handling Invalid Parameters error...")
//	    // We can still access the specific error details if needed:
//	    if rpcErr, ok := err.(jsonrpc2.Error); ok {
//	        fmt.Printf("  Details: %v\n", rpcErr.Data.RawMessage()) // Output: Details: "Parameters cannot be null"
//	    }
//	}
func (e Error) Is(t error) bool {
	// Check if target is Error or *Error
	var jerr Error
	if errors.As(t, &jerr) {
		// Compare codes only if both errors are considered 'present' (not zero value)
		return e.present && jerr.present && e.Code == jerr.Code
	}

	return false
}

// IsZero reports whether the Error instance is the zero value.
// A zero-value Error typically indicates that no error was present in a
// JSON-RPC response or that the Error struct was not initialized correctly.
// Errors created using [NewError] or [NewErrorWithData] will not be zero.
func (e *Error) IsZero() bool {
	// Check the internal 'present' flag.
	return !e.present
}

// Error implements the standard Go `error` interface.
// It returns the [Error.Message] string, making the [Error] type usable
// wherever a standard `error` is expected.
// Note: This means the Code and Data are not included in the basic error string.
// Use type assertion or errors.As to access the full Error details if needed.
func (e Error) Error() string {
	return e.Message
}

// UnmarshalJSON implements the [encoding/json.Unmarshaler] interface.
// This custom unmarshaler ensures that the internal `present` flag is set
// correctly when an Error object is successfully unmarshaled from JSON,
// distinguishing it from a zero-value Error struct. It uses the package-level
// [Unmarshal] function.
func (e *Error) UnmarshalJSON(b []byte) error {
	// Define a temporary struct matching the JSON structure.
	// Use a pointer for ErrorData to detect its presence correctly.
	var tmp struct {
		Code    int64      `json:"code"`
		Message string     `json:"message"`
		Data    *ErrorData `json:"data,omitempty"` // Pointer to check presence
	}

	// Unmarshal into the temporary struct using the package-level Unmarshal func.
	if err := Unmarshal(b, &tmp); err != nil {
		return err // Return underlying unmarshal error
	}

	// Successfully unmarshaled, populate the receiver Error.
	e.present = true // Mark as present/initialized.
	e.Code = tmp.Code
	e.Message = tmp.Message
	if tmp.Data != nil {
		// If data was present in JSON, copy it.
		e.Data = *tmp.Data
	} else {
		// If data was not present, ensure Data is its zero value.
		e.Data = ErrorData{}
	}

	return nil
}
