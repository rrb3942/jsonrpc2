package jsonrpc2

import (
	"encoding/json"
	"errors"
	"fmt"
)

var ErrIDNotANumber = errors.New("ID is not a number")

// ID represents a jsonrpc2 id used for identifying requests and their responses.
//
// Valid json types for IDs are strings, integers, floats (discouraged), and null (discouraged).
//
// Numeric values are always decoded to a [json.Number].
//
// To avoid precision and other issues with floats, they are required to be in a [json.Number] format, and the floats are not operated on directly.
type ID struct {
	value   any
	present bool
}

// To use a float as an ID, first marshal it to a [json.Number].
func NewID[V int64 | string | json.Number](v V) ID {
	return ID{present: true, value: v}
}

// NewNullID returns a new non-zero ID set to null.
// null ids are discouraged, but may be used when generateing responses to malformed requests.
func NewNullID() ID {
	return ID{present: true}
}

// Equal compares two IDs and returns true if they are equivalent.
//
// Zero value IDs (as returned by [ID.IsZero]) are never equal, even if both IDs are equal otherwise.
//
// Two IDs with the null value are equal if both IDs are null.
//
// String values are equal if both IDs are string types and are strictly equivalent (==).
//
// Numeric values are never equal to a string value.
//
// If numeric values are both [json.Number], they are compared using string comparison (==). This is what is used to compare floats.
//
// int64 values are compared directly (==).
// A [json.Number] will automatically be converted to an int64 for comparison IF the [json.Number] is an integer.
func (id *ID) Equal(t ID) bool {
	// Unset values are never equal
	if id.IsZero() || t.IsZero() {
		return false
	}

	if id.IsNull() {
		return t.IsNull()
	}

	switch v := id.value.(type) {
	case json.Number:
		// Both numbers, do straight cmp
		if jn, ok := t.Number(); ok {
			return v == jn
		}

		// If target is int64 we can try to match
		if in, err := t.Int64(); err == nil {
			if vi, err := v.Int64(); err == nil {
				return vi == in
			}
		}
	case int64:
		if in, err := t.Int64(); err == nil {
			return v == in
		}
	case string:
		if s, ok := t.String(); ok {
			return v == s
		}
	}

	return false
}

// IsZero returns true if ID is not set to a value of any kind.
// If IsZero() is true, it is equivalent to the ID not being present.
func (id *ID) IsZero() bool {
	return !id.present
}

// IsNull() returns true if the ID is set to a null or nil value.
func (id *ID) IsNull() bool {
	if id.present {
		return id.value == nil
	}

	return false
}

// RawValue returns the raw underlying value of the ID. It may be nil.
func (id *ID) RawValue() any {
	return id.value
}

// String returns a string representation of the id value.
// The bool will be true if the underlying type was an explicit string and a valid value was returned.
// If the ID is value is null the string value will be empty bool will be false.
func (id *ID) String() (string, bool) {
	if v, ok := id.value.(string); ok {
		return v, true
	}

	return "", false
}

// Number returns the value as a json.Number
// If the value is not a json.Number the bool will return false.
// Raw string and integer values will not be converted.
func (id *ID) Number() (json.Number, bool) {
	if id.value != nil {
		if num, ok := id.value.(json.Number); ok {
			return num, ok
		}
	}

	return json.Number(""), false
}

// Int64 returns the value as a int64.
// String values will not be automatically converted.
// The returned error indicates if the returned value is valid.
func (id *ID) Int64() (int64, error) {
	if num, ok := id.Number(); ok {
		return num.Int64()
	}

	if i, ok := id.value.(int64); ok {
		return i, nil
	}

	return 0, ErrIDNotANumber
}

// UnmarshalJSON implements the [json.Unmarshaler] interface.
func (id *ID) UnmarshalJSON(data []byte) error {
	switch {
	// String value
	case data[0] == '"':
		var str string
		if err := Unmarshal(data, &str); err != nil {
			return fmt.Errorf("%w (%w)", ErrDecoding, err)
		}

		id.value = str
		id.present = true
	// null
	case string(data) == "null":
		id.present = true
	default:
		// Otherwise must be a number
		var num json.Number
		if err := Unmarshal(data, &num); err != nil {
			return fmt.Errorf("%w (%w)", ErrDecoding, err)
		}

		id.value = num
		id.present = true
	}

	return nil
}

// MarshalJSON implements the [json.Marshaler] interface.
func (id *ID) MarshalJSON() ([]byte, error) {
	buf, err := Marshal(id.value)

	if err != nil {
		return nil, fmt.Errorf("%w (%w)", ErrEncoding, err)
	}

	return buf, nil
}
