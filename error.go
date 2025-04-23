package jsonrpc2

import (
	"errors"
)

var (
	ErrParse            = NewError(-32700, "Parse Error")
	ErrInvalidRequest   = NewError(-32600, "Invalid Request")
	ErrMethodNotFound   = NewError(-32601, "Method not found")
	ErrInvalidParams    = NewError(-32602, "Invalid params")
	ErrInternalError    = NewError(-32603, "Internal Error")
	ErrServerOverloaded = NewError(-32000, "Server Overloaded")
)

// RPCError is the internal representation of an error used by [Error].
type RPCError struct {
	Data    ErrorData `json:"data,omitempty,omitzero"`
	Message string    `json:"message"`
	Code    int64     `json:"code"`
}

// Error represents a jsonrpc2 error object
//
// [Error] supports the go error interface and may be used as a normal error.
//
//nolint:govet //We want order to match spec examples, even if not required
type Error struct {
	present bool
	err     RPCError
}

// NewError returns a new [Error] with its Code and Message fields assigned to the given values.
func NewError(code int64, msg string) Error {
	return Error{present: true, err: RPCError{Code: code, Message: msg}}
}

// NewErrorWithData is the same as [NewError] but also allows setting of the Data field.
func NewErrorWithData(code int64, msg string, data any) Error {
	return Error{present: true, err: RPCError{Code: code, Message: msg, Data: NewErrorData(data)}}
}

func asError(e error) Error {
	var je Error

	if errors.As(e, &je) {
		return je
	}

	return ErrInternalError.WithData(e.Error())
}

// Code returns the code present in the error.
func (e *Error) Code() int64 {
	return e.err.Code
}

// Message returns the message present in the error.
func (e *Error) Message() string {
	return e.err.Message
}

// Data returns the data present in the error.
func (e *Error) Data() *ErrorData {
	return &e.err.Data
}

// WithData returns a copy of the current [Error] with its Data field set to data.
func (e *Error) WithData(data any) Error {
	return Error{present: true, err: RPCError{Code: e.err.Code, Message: e.err.Message, Data: NewErrorData(data)}}
}

// Returns true if t is of type [Error] and their Code fields match.
func (e Error) Is(t error) bool {
	if jerr, ok := t.(Error); ok {
		return e.err.Code == jerr.err.Code
	}

	if jerr, ok := t.(*Error); ok {
		return e.err.Code == jerr.err.Code
	}

	return false
}

// IsZero returns true if the error is empty.
func (e *Error) IsZero() bool {
	return !e.present
}

// Error implements the error interface.
func (e Error) Error() string {
	return e.err.Message
}

// UnmarshalJSON implements [json.Unmarshaler].
func (e *Error) UnmarshalJSON(b []byte) error {
	if err := Unmarshal(b, &e.err); err != nil {
		return err
	}

	e.present = true

	return nil
}

// MarshalJSON implements [json.Marshaler].
func (e *Error) MarshalJSON() ([]byte, error) {
	return Marshal(&e.err)
}
