package jsonrpc2

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrInvalidParameters indicates that the provided parameters are invalid according to the JSON-RPC 2.0 specification (e.g., not an object or array).
var ErrInvalidParameters = errors.New("Invalid parameters")

// ErrNotRawMessage indicates that an operation expected the internal value to be a [json.RawMessage], but it was not.
var ErrNotRawMessage = errors.New("Value is it not a raw message")

var errInvalidParamDecode = fmt.Errorf("%w (%w)", ErrDecoding, ErrInvalidParameters)

// Params represent the params member of a jsonrpc2 [Request] ([Request.Params]).
//
// Params always decodes to a [json.RawMessage] internally.
//
// Params must be a json object or array.
type Params struct {
	value any
}

// NewParamsArray returns a new [Params] with its value set to v.
// The underlying value will be the provided slice.
//
// Example:
//
//	params := jsonrpc2.NewParamsArray([]any{1, "hello", true})
//	// params.Value() will be []any{1, "hello", true}
func NewParamsArray[V any, P ~[]V](v P) Params {
	return Params{value: v}
}

// NewParamsObject returns a new [Params] with its value set to v.
// The underlying value will be the provided map.
//
// Example:
//
//	user := map[string]any{"name": "Alice", "age": 30}
//	params := jsonrpc2.NewParamsObject(user)
//	// params.Value() will be map[string]any{"name":"Alice", "age":30}
func NewParamsObject[K comparable, V any, P ~map[K]V](v P) Params {
	return Params{value: v}
}

// NewParamsRaw returns a new [Params] wrapping the provided [json.RawMessage].
// This is useful when you have parameters already encoded as JSON and want to
// include them directly in a request without further encoding/decoding cycles
// until the final processing stage.
//
// It is also used when you need to Marshal a custom struct to use a parameters.
func NewParamsRaw(v json.RawMessage) Params {
	// Note: Internally uses NewParamsArray, treating RawMessage as []byte.
	return NewParamsArray(v)
}

// RawMessage returns the internally stored [json.RawMessage].
//
// If the stored value is not a [json.RawMessage] nil is returned.
func (p *Params) RawMessage() json.RawMessage {
	if raw, ok := p.value.(json.RawMessage); ok {
		return raw
	}

	return nil
}

// Value returns the raw internal value. May be a raw go type, [json.RawMessage], or nil.
func (p *Params) Value() any {
	return p.value
}

// TypeHint provides a hint for the type of json data contained within the [Params]. See [TypeHint].
//
// Returns [TypeNotJSON] if the underlying type is not a [json.RawMessage].
func (p *Params) TypeHint() TypeHint {
	if m, ok := p.value.(json.RawMessage); ok {
		return jsonHintType(m)
	}

	return TypeNotJSON
}

// Unmarshal will unmarshal the internally stored [json.RawMessage] into v, returning any errors.
//
// If a [json.RawMessage] is not stored internally, it will return [ErrNotRawMessage].
func (p *Params) Unmarshal(v any) error {
	if raw, ok := p.value.(json.RawMessage); ok {
		return Unmarshal(raw, v)
	}

	return ErrNotRawMessage
}

// IsZero returns if [Params] represents its zero value.
//
// IsZero is true if the internal value is nil or it contains a [json.RawMessage] of length 0.
func (p *Params) IsZero() bool {
	if p.value == nil {
		return true
	}

	if raw, ok := p.value.(json.RawMessage); ok {
		return len(raw) == 0
	}

	return false
}

// UnmarshalJSON implements the [json.Unmarshaler] interface.
// It ensures that the incoming JSON data represents either a JSON object or array,
// as required by the JSON-RPC 2.0 specification for the 'params' field.
// The raw JSON data is stored internally for potential later unmarshalling into a specific Go type via the [Params.Unmarshal] method.
func (p *Params) UnmarshalJSON(data []byte) error {
	// Must be an object or array
	switch jsonHintType(data) {
	case TypeObject, TypeArray:
		var raw json.RawMessage
		if err := Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("%w (%w)", ErrDecoding, err)
		}

		p.value = raw

		return nil
	default:
		return errInvalidParamDecode
	}
}

// MarshalJSON implements the [json.Marshaler] interface.
func (p *Params) MarshalJSON() ([]byte, error) {
	return Marshal(p.value)
}
