package jsonrpc2

import (
	"encoding/json"
	"errors"
	"fmt"
)

var ErrInvalidParameters = errors.New("Invalid parameters")
var ErrNotRawMessage = errors.New("Value is it not a raw message")
var errInvalidParamDecode = fmt.Errorf("%w (%w)", ErrDecoding, ErrInvalidParameters)

// Params represent the params member of a jsonrpc2 [Request]
//
// Params always decodes to a [json.RawMessage] internally.
//
// Params must be a json object or array.
type Params struct {
	value any
}

// NewParamsArray returns a new [Params] with its value set to v.
//
// v must be a slice type.
func NewParamsArray[V any, P ~[]V](v P) Params {
	return Params{value: v}
}

// NewParamsObj returns a new [Params] with its value set to v.
//
// v must be a map type.
func NewParamsObj[K comparable, V any, P ~map[K]V](v P) Params {
	return Params{value: v}
}

// NewParamsRaw returns a new [Params] with its value set to v.
//
// It only accepts a json.RawMessage.
func NewParamsRaw(v json.RawMessage) Params {
	return NewParamsArray(v)
}

// RawMessage returns the [json.RawMessage] stored.
//
// If the stored value is not a [json.RawMessage] nil is returned.
func (p *Params) RawMessage() json.RawMessage {
	if raw, ok := p.value.(json.RawMessage); ok {
		return raw
	}

	return nil
}

// TypeHint provides a hint for the type of json data contained. See [TypeHint].
//
// Returns [TypeNotJSON] if boxed type is not a [json.RawMessage].
func (p *Params) TypeHint() TypeHint {
	if m, ok := p.value.(json.RawMessage); ok {
		return jsonHintType(m)
	}

	return TypeNotJSON
}

// Unmarshal will unmarshal the store [json.RawMessage] into v, returning any errors.
//
// If a [json.RawMessage] is not stored it will return [ErrNotRawMessage].
func (p *Params) Unmarshal(v any) error {
	if raw, ok := p.value.(json.RawMessage); ok {
		return Unmarshal(raw, v)
	}

	return ErrNotRawMessage
}

// IsZero returns if Params its zero value.
//
// IsZero is true if Params contains nil or a [json.RawMessage] or length 0.
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
func (p *Params) UnmarshalJSON(data []byte) error {
	// Must be an object or array
	switch data[0] {
	case '{', '[':
		var raw json.RawMessage
		if err := raw.UnmarshalJSON(data); err != nil {
			return fmt.Errorf("%w (%w)", ErrDecoding, err)
		}

		p.value = raw

		return nil
	}

	return errInvalidParamDecode
}

// MarshalJSON implements the [json.Marshaler] interface.
func (p *Params) MarshalJSON() ([]byte, error) {
	return Marshal(p.value)
}
