package jsonrpc2

import (
	"encoding/json"
	"errors"
	"fmt"
)

var ErrIDNotANumber = errors.New("ID is not a number")

// ID represents a jsonrpc2 id used for identifying requests and their responses.
//
// Valid underlying value types for IDs are strings, integers, floats (discouraged), and null (discouraged).
type ID struct {
	value   any
	present bool
}

// NewID returns a new non-zero ID set to v.
// Only certain types are valid for use as an ID.
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
// Number value IDs are equivalent if they are the same type and pass a straight comparison (==).
// The only exception is that, if possible, json.Number values will be converted to compatible types before comparison.
//
// json.Number values are compared using string comparison if both types are json.Number.
//
// String values are equal if both IDs are string types and are strictly equivalent (==).
//
// A json.Number value is never equal to an equivalient string value.
func (i *ID) Equal(t ID) bool {
	// Unset values are never equal
	if i.IsZero() || t.IsZero() {
		return false
	}

	if i.IsNull() {
		return t.IsNull()
	}

	switch v := i.value.(type) {
	case json.Number:
		if jn, ok := t.Number(); ok {
			return v == jn
		}
	case int64:
		if in, err := t.Int64(); err != nil {
			return v == in
		}
	case float64:
		if fn, err := t.Float64(); err != nil {
			return v == fn
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
func (i *ID) IsZero() bool {
	return !i.present
}

// IsNull() returns true if the ID is set to a null or nil value.
func (i *ID) IsNull() bool {
	if i.present {
		return i.value == nil
	}

	return false
}

// RawValue returns the raw underlying value of the ID. It may be nil.
func (i *ID) RawValue() any {
	return i.value
}

// String returns a string representation of the id value.
// The bool will be true if the underlying type was an explicit string and a valid value was returned.
// If the ID is value is null the string value will be empty bool will be false.
func (i *ID) String() (string, bool) {
	if v, ok := i.value.(string); ok {
		return v, true
	}

	return "", false
}

// Number returns the value as a json.Number
// If the value is not a json.Number the bool will return false.
// Raw string values will not be converted.
func (i *ID) Number() (json.Number, bool) {
	if i.value != nil {
		num, ok := i.value.(json.Number)

		return num, ok
	}

	return json.Number(""), false
}

// Float64 returns the value as a float64.
// String values will not be automatically converted.
// The returned error indicates if the returned value is valid.
func (i *ID) Float64() (float64, error) {
	if num, ok := i.Number(); ok {
		return num.Float64()
	}

	if f, ok := i.value.(float64); ok {
		return f, nil
	}

	return 0, ErrIDNotANumber
}

// Int64 returns the value as a int64.
// String values will not be automatically converted.
// The returned error indicates if the returned value is valid.
func (i *ID) Int64() (int64, error) {
	if num, ok := i.Number(); ok {
		return num.Int64()
	}

	if i, ok := i.value.(int64); ok {
		return i, nil
	}

	return 0, ErrIDNotANumber
}

// UnmarshalJSON implements the [json.Unmarshaler] interface.
func (i *ID) UnmarshalJSON(data []byte) error {
	switch {
	// String value
	case data[0] == '"':
		var str string
		if err := json.Unmarshal(data, &str); err != nil {
			return fmt.Errorf("%w (%w)", ErrDecoding, err)
		}

		i.value = str
		i.present = true
	// null
	case string(data) == "null":
		i.present = true
	default:
		// Otherwise must be a number
		var num json.Number
		if err := json.Unmarshal(data, &num); err != nil {
			return fmt.Errorf("%w (%w)", ErrDecoding, err)
		}

		i.value = num
		i.present = true
	}

	return nil
}

// MarshalJSON implements the [json.Marshaler] interface.
func (i *ID) MarshalJSON() ([]byte, error) {
	buf, err := json.Marshal(i.value)

	if err != nil {
		return nil, fmt.Errorf("%w (%w)", ErrEncoding, err)
	}

	return buf, nil
}
