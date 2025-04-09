package jsonrpc2

import (
	"encoding/json"
	"errors"
	"fmt"
)

var ErrEmptyData = errors.New("data is empty")

// Data generically wraps arbitrary data but always unmarshals into [json.RawMessage] internally.
//
// If a [json.RawMessage] is stored internally, it is directly used for marshaling.
type Data struct {
	value any
}

// RawMessage returns [json.RawMessage] stored internally if present.
//
// RawMessage may only be valid after an unmarshalling, or if a [json.RawMessage]
// was stored directly.
func (d *Data) RawMessage() json.RawMessage {
	if raw, ok := d.value.(json.RawMessage); ok {
		return raw
	}

	return nil
}

// Value returns the underlying value as stored when created with a New* function.
//
// The value may be nil if not set or a nil was stored.
func (d *Data) Value() any {
	return d.value
}

// Unmarshal will call unmarshal the internal [json.RawMessage] into v, returning any errors.
//
// If the internal value is nil, [ErrEmptyData] is returned and v is untouched.
//
// If there is no internal [json.RawMessage] [ErrNotRawMessage] is returned and v is untouched.
func (d *Data) Unmarshal(v any) error {
	switch vt := d.value.(type) {
	case json.RawMessage:
		return json.Unmarshal(vt, v)
	case nil:
		return ErrEmptyData
	}

	return ErrNotRawMessage
}

// IsZero returns true if the underlying value is nil or zero length [json.RawMessage].
func (d *Data) IsZero() bool {
	if d.value == nil {
		return true
	}

	if raw, ok := d.value.(json.RawMessage); ok {
		return len(raw) == 0
	}

	return false
}

// UnmarshalJSON implements the [json.Unmarshaler] interface.
func (d *Data) UnmarshalJSON(data []byte) error {
	var raw json.RawMessage
	if err := raw.UnmarshalJSON(data); err != nil {
		return fmt.Errorf("%w (%w)", ErrDecoding, err)
	}

	d.value = raw

	return nil
}

// MarshalJSON implements the [json.Marshaler] interface.
func (d *Data) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.value)
}
