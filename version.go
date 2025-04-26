package jsonrpc2

import (
	"errors"
	"fmt"
)

// ProtocolVersion defines the required string value ("2.0") for the "jsonrpc" field
// in JSON-RPC 2.0 requests and responses.
const ProtocolVersion = "2.0"

// ErrWrongProtocolVersion is returned by [Version.UnmarshalJSON] when the "jsonrpc"
// field is present but does not contain the exact string "2.0".
var ErrWrongProtocolVersion = errors.New("wrong protocol version for jsonrpc2")

// errWrongProtoVerDecode wraps ErrWrongProtocolVersion for internal use during decoding.
var errWrongProtoVerDecode = fmt.Errorf("%w (%w)", ErrDecoding, ErrWrongProtocolVersion)

// Version represents the "jsonrpc" field required in JSON-RPC 2.0 requests and responses.
// Its primary role is to ensure the correct protocol version ("2.0") is specified
// during JSON unmarshaling.
//
// The zero value of Version is considered invalid until successfully unmarshaled
// from the correct JSON string `"2.0"`.
//
// See: https://www.jsonrpc.org/specification#request_object
// See: https://www.jsonrpc.org/specification#response_object
type Version struct {
	// present tracks whether UnmarshalJSON successfully validated the "2.0" string.
	present bool
}

// IsValid returns true if the Version was successfully unmarshaled from the
// JSON string `"2.0"`. A zero-value Version or one unmarshaled from incorrect
// data will return false.
func (v *Version) IsValid() bool {
	return v.present
}

// UnmarshalJSON implements the [json.Unmarshaler] interface.
// It expects the input `data` to be a JSON string containing exactly "2.0".
// If the input is not a string or does not match [ProtocolVersion], it returns
// an error ([ErrDecoding] or [ErrWrongProtocolVersion]).
// On success, it marks the Version as valid (so [IsValid] returns true).
func (v *Version) UnmarshalJSON(data []byte) error {
	var str string
	// Use the package-level Unmarshal
	if err := Unmarshal(data, &str); err != nil {
		// Handle JSON decoding errors (e.g., not a string, invalid syntax)
		return fmt.Errorf("%w: expected string \"2.0\" (%w)", ErrDecoding, err)
	}

	if str != ProtocolVersion {
		// The string value was wrong
		return errWrongProtoVerDecode
	}

	// Mark as valid only if the string is exactly "2.0"
	v.present = true
	return nil
}

// MarshalJSON implements the [json.Marshaler] interface.
// It always returns the JSON string `"2.0"`, regardless of the Version's
// internal state ([IsValid]). This ensures that marshaled requests/responses
// always include the correct protocol version string.
func (v Version) MarshalJSON() ([]byte, error) { // Note: receiver is value type
	// Use the package-level Marshal
	buf, err := Marshal(ProtocolVersion) // Always marshal the constant "2.0"
	if err != nil {
		// This should ideally not happen for a simple string marshal.
		return nil, fmt.Errorf("%w: failed to marshal protocol version string (%w)", ErrEncoding, err)
	}
	return buf, nil
}
