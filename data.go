package jsonrpc2

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrEmptyData is returned by [Data.Unmarshal] when attempting to unmarshal
// from a Data instance that represents JSON `null` or was not populated.
var ErrEmptyData = errors.New("data is empty")

// Data acts as a container for arbitrary JSON data, typically used within
// other structures like [ErrorData] or [Result].
//
// When unmarshalling JSON into a struct containing a Data field, the corresponding
// JSON value is stored internally as a [json.RawMessage]. This allows delaying
// the actual parsing of the data until it's needed.
//
// Use the [Data.Unmarshal] method to decode the contained data into a specific Go type.
type Data struct {
	value   any  // Holds the data, typically json.RawMessage after unmarshalling.
	present bool // Tracks if the Data field was present in the JSON input.
}

// RawMessage returns the underlying [json.RawMessage] if the Data container
// holds one (which is the typical case after unmarshalling JSON).
// If the container holds a different type or is empty/null, it returns nil.
//
// Example:
//
//	var d Data
//	_ = json.Unmarshal([]byte(`{"key":"value"}`), &d)
//	raw := d.RawMessage() // raw contains json.RawMessage(`{"key":"value"}`)
func (d *Data) RawMessage() json.RawMessage {
	// Check if the internal value is specifically json.RawMessage
	if raw, ok := d.value.(json.RawMessage); ok {
		return raw
	}

	return nil
}

// Value returns the underlying Go value stored within the Data container.
// After unmarshalling JSON, this will typically be a [json.RawMessage] or nil (for JSON null).
// This method is less commonly used than [Data.Unmarshal].
func (d *Data) Value() any {
	return d.value
}

// TypeHint inspects the contained [json.RawMessage] (if present) and returns a hint
// about the top-level JSON type (object, array, string, etc.). See [TypeHint] for possible values.
// Returns [TypeNotJSON] if the container doesn't hold a [json.RawMessage].
//
// Example:
//
//	var d Data
//	_ = json.Unmarshal([]byte(`[1, 2, 3]`), &d)
//	hint := d.TypeHint() // hint == jsonrpc2.TypeArray
func (d *Data) TypeHint() TypeHint {
	if m, ok := d.value.(json.RawMessage); ok {
		return HintType(m) // jsonHintType handles various JSON types including null
	}
	// If value is not json.RawMessage, we can't determine the JSON type.
	return TypeNotJSON
}

// Unmarshal decodes the JSON data held within the Data container into the value pointed to by v.
// This is the primary method for accessing the data in a structured way.
//
// It returns:
//   - nil: If unmarshalling is successful.
//   - [ErrEmptyData]: If the Data container is empty.
//   - [ErrNotRawMessage]: If the container does not hold a [json.RawMessage] (less common).
//   - An error from the underlying JSON decoder if the data is invalid or doesn't match `v`.
//
// Example:
//
//	var d Data
//	_ = json.Unmarshal([]byte(`{"name":"example","value":10}`), &d)
//
//	type MyStruct struct {
//		Name string `json:"name"`
//		Value int `json:"value"`
//	}
//	var target MyStruct
//	err := d.Unmarshal(&target)
//	if err == nil {
//		fmt.Println(target.Name, target.Value) // Output: example 10
//	}
func (d *Data) Unmarshal(v any) error {
	switch vt := d.value.(type) {
	case json.RawMessage:
		// Attempt to unmarshal the raw JSON into the provided variable v.
		return Unmarshal(vt, v)
	case nil:
		// If the internal value is nil (e.g., Data struct was zero value or held explicit nil)
		return ErrEmptyData
	}

	// If the internal value is not json.RawMessage (e.g., manually assigned non-JSON value)
	return ErrNotRawMessage
}

// IsZero returns true if the Data struct is its zero value, meaning it was likely
// omitted in the source JSON or not populated. It returns false if the Data struct
// holds a value, even if that value is JSON `null`.
func (d *Data) IsZero() bool {
	// `present` is true if UnmarshalJSON successfully processed *some* JSON input,
	// even if that input was `null`.
	return !d.present
}

// UnmarshalJSON implements the [json.Unmarshaler] interface.
func (d *Data) UnmarshalJSON(data []byte) error {
	var raw json.RawMessage
	if err := Unmarshal(data, &raw); err != nil {
		// This captures json syntax errors, etc.
		return fmt.Errorf("failed to unmarshal into raw message: %w", err)
	}

	// Store the raw message, whatever it is (including "null").
	d.value = raw
	// Mark as present because we successfully processed *some* JSON input.
	d.present = true

	return nil
}

// MarshalJSON implements the [json.Marshaler] interface.
// It marshals the contained Go value into JSON. If the value is [json.RawMessage],
// it's output directly. If the value is nil (and the field was present), it marshals as JSON `null`.
func (d *Data) MarshalJSON() ([]byte, error) {
	if !d.present {
		return []byte("null"), nil
	}

	// Marshal the stored value (which could be nil, json.RawMessage, or something else).
	// The package-level Marshal handles nil correctly (outputs "null").
	return Marshal(d.value)
}
