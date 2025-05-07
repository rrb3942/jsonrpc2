package jsonrpc2

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrIDNotANumber is returned by [ID.Int64] when the underlying ID value is not
// an int64 or a json.Number representing an integer.
var ErrIDNotANumber = errors.New("ID is not a number")

// ID represents a JSON-RPC 2.0 request ID.
//
// According to the specification, an ID should be a String, a Number (floats discouraged), or Null (discouraged).
// This type enforces these requirements.
//
// Upon unmarshalling JSON:
//   - JSON strings become Go strings.
//   - JSON numbers (integers or floats) become [json.Number].
//   - JSON null becomes nil internally.
//
// Use the constructor functions [NewID] (for string, int64, or json.Number)
// and [NewNullID] (for null) to create ID instances programmatically.
// Avoid creating ID structs directly.
//
// See: https://www.jsonrpc.org/specification#request_object
type ID struct {
	value   any  // Stores the actual ID value (string, int64, json.Number, or nil).
	present bool // Tracks if the ID was explicitly set (even if set to null), distinguishing from a zero-value ID struct.
}

// NewID creates a new ID with the given value.
// The type parameter V can be int64, string, or json.Number.
// Floats are not directly supported; represent them as json.Number if needed.
//
// Example:
//
//	idInt := jsonrpc2.NewID(int64(123))
//	idStr := jsonrpc2.NewID("request-5")
//	idNum := jsonrpc2.NewID(json.Number("456.7"))
func NewID[V int64 | string | json.Number](v V) ID {
	return ID{present: true, value: v}
}

// NewNullID creates an ID representing the JSON `null` value.
// This is distinct from a zero-value [ID] struct (where [ID.IsZero] is true).
// Null IDs are primarily used in responses to requests that could not be parsed
// correctly to determine their original ID.
//
// Example:
//
//	nullID := jsonrpc2.NewNullID()
//	fmt.Println(nullID.IsNull()) // Output: true
//	fmt.Println(nullID.IsZero()) // Output: false
func NewNullID() ID {
	return ID{present: true} // value is implicitly nil
}

// Equal compares two IDs for equivalence according to JSON-RPC rules.
//
// Rules:
//   - Zero-value IDs ([ID.IsZero] is true) are never equal to any other ID, including another zero-value ID.
//   - Two null IDs ([ID.IsNull] is true) are equal.
//   - A null ID is never equal to a non-null ID.
//   - Two string IDs are equal if their string values are identical (==).
//   - Two numeric IDs ([int64] or [json.Number]) are compared numerically:
//   - If both are [json.Number], they are compared as strings (exact representation matters, e.g., "1" != "1.0").
//   - If one is [int64] and the other is [json.Number], the [json.Number] is converted to int64 (if possible) for comparison.
//   - String IDs are never equal to numeric IDs.
//
// Example:
//
//	id1 := jsonrpc2.NewID(int64(1))
//	id1Str := jsonrpc2.NewID("1")
//	id1Num := jsonrpc2.NewID(json.Number("1"))
//	idNull := jsonrpc2.NewNullID()
//
//	fmt.Println(id1.Equal(id1Num))    // Output: true
//	fmt.Println(id1.Equal(id1Str))    // Output: false
//	fmt.Println(idNull.Equal(idNull)) // Output: true
//	fmt.Println(id1.Equal(idNull))    // Output: false
func (id *ID) Equal(t ID) bool {
	// Zero-value (uninitialized) IDs are never equal to anything.
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

// IsZero returns true if the ID struct is the zero value (i.e., uninitialized).
// This is distinct from an ID that has been explicitly set to `null` using [NewNullID].
// A zero ID typically indicates an absence of an ID field during processing before marshaling/unmarshaling.
//
// Example:
//
//	var zeroID jsonrpc2.ID
//	nullID := jsonrpc2.NewNullID()
//	fmt.Println(zeroID.IsZero()) // Output: true
//	fmt.Println(nullID.IsZero()) // Output: false
func (id *ID) IsZero() bool {
	return !id.present
}

// IsNull returns true if the ID represents the JSON `null` value.
// This happens if the ID was created with [NewNullID] or unmarshaled from JSON `null`.
//
// Example:
//
//	nullID := jsonrpc2.NewNullID()
//	intID := jsonrpc2.NewID(int64(1))
//	fmt.Println(nullID.IsNull()) // Output: true
//	fmt.Println(intID.IsNull())  // Output: false
func (id *ID) IsNull() bool {
	// Check present first to distinguish from zero value.
	// A zero value's `value` is also nil, but it's not considered "null" in the JSON-RPC sense.
	return id.present && id.value == nil
}

// Value returns the underlying Go value of the ID.
// The returned type will be one of: string, int64, json.Number, or nil.
// Returns nil if the ID is the zero value ([ID.IsZero] is true).
//
// Example:
//
//	idInt := jsonrpc2.NewID(int64(123))
//	idStr := jsonrpc2.NewID("req-abc")
//	idNull := jsonrpc2.NewNullID()
//	fmt.Printf("%T\n", idInt.Value())  // Output: int64
//	fmt.Printf("%T\n", idStr.Value())  // Output: string
//	fmt.Printf("%T\n", idNull.Value()) // Output: <nil>
func (id *ID) Value() any {
	// If not present (zero value), return nil explicitly.
	if !id.present {
		return nil
	}

	return id.value
}

// String attempts to return the ID value as a string.
// It returns the string and `true` only if the underlying value is a string.
// Otherwise, it returns an empty string and `false`.
//
// Example:
//
//	idStr := jsonrpc2.NewID("request-5")
//	idInt := jsonrpc2.NewID(int64(1))
//	s, ok := idStr.String() // s == "request-5", ok == true
//	_, ok = idInt.String()  // ok == false
func (id *ID) String() (string, bool) {
	if !id.present {
		return "", false
	}

	s, ok := id.value.(string)

	return s, ok
}

// Number attempts to return the ID value as a [json.Number].
// It returns the number and `true` only if the underlying value is a [json.Number]
// (typically resulting from unmarshalling a JSON number).
// Otherwise, it returns an empty [json.Number] and `false`.
// It does *not* convert int64 or string values.
//
// Example:
//
//	var idNum jsonrpc2.ID
//	_ = json.Unmarshal([]byte(`123.45`), &idNum) // Unmarshals to json.Number
//	idInt := jsonrpc2.NewID(int64(1))
//	n, ok := idNum.Number() // n == "123.45", ok == true
//	_, ok = idInt.Number()  // ok == false
func (id *ID) Number() (json.Number, bool) {
	if !id.present || id.value == nil {
		return "", false
	}

	num, ok := id.value.(json.Number)

	return num, ok
}

// Int64 attempts to return the ID value as an int64.
// It succeeds if the underlying value is an int64 or a [json.Number] that represents
// a valid integer within the int64 range.
// It returns an error ([ErrIDNotANumber] or a `strconv` error) if the value is
// not a number, is a non-integer number (e.g., "1.2"), or is outside the int64 range.
// It does *not* attempt to parse string IDs.
//
// Example:
//
//	idInt := jsonrpc2.NewID(int64(123))
//	var idNumInt jsonrpc2.ID
//	_ = json.Unmarshal([]byte(`456`), &idNumInt)
//	var idNumFloat jsonrpc2.ID
//	_ = json.Unmarshal([]byte(`78.9`), &idNumFloat)
//	idStr := jsonrpc2.NewID("abc")
//
//	i, err := idInt.Int64()      // i == 123, err == nil
//	i, err = idNumInt.Int64()    // i == 456, err == nil
//	_, err = idNumFloat.Int64()  // err != nil (strconv.ErrSyntax)
//	_, err = idStr.Int64()       // err == jsonrpc2.ErrIDNotANumber
func (id *ID) Int64() (int64, error) {
	if !id.present {
		return 0, ErrIDNotANumber
	}

	switch v := id.value.(type) {
	case int64:
		return v, nil
	case json.Number:
		// Attempt conversion from json.Number
		return v.Int64() // Returns strconv errors on failure (syntax, range)
	default:
		// Includes string, nil (null ID)
		return 0, ErrIDNotANumber
	}
}

// UnmarshalJSON implements the [json.Unmarshaler] interface.
// It correctly decodes JSON strings, numbers (as json.Number), and null
// into the ID struct. It returns an error for other JSON types (boolean, object, array).
func (id *ID) UnmarshalJSON(data []byte) error {
	switch HintType(data) {
	case TypeNull:
		id.present = true
	case TypeString:
		var str string
		if err := Unmarshal(data, &str); err != nil {
			return fmt.Errorf("%w: %w", ErrDecoding, err)
		}

		id.value = str
		id.present = true
	case TypeNumber:
		// Otherwise must be a number
		var num json.Number
		if err := Unmarshal(data, &num); err != nil {
			return fmt.Errorf("%w: %w", ErrDecoding, err)
		}

		id.value = num
		id.present = true
	default:
		return fmt.Errorf("%w: invalid type for ID", ErrDecoding)
	}

	return nil
}

// MarshalJSON implements the [json.Marshaler] interface.
// It serializes the ID into the appropriate JSON representation (string, number, or null).
// A zero-value ID ([ID.IsZero] is true) marshals as JSON `null`.
func (id *ID) MarshalJSON() ([]byte, error) {
	// A zero-value ID marshals to null, similar to how omitempty works.
	// An explicitly set null ID (value == nil, present == true) also marshals to null.
	if !id.present || id.value == nil {
		return []byte("null"), nil
	}

	buf, err := Marshal(id.value)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrEncoding, err)
	}

	return buf, nil
}
