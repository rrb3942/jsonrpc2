package jsonrpc2

import (
	"bytes"
	"encoding/json"
)

// TypeHint provides a quick classification of the likely top-level JSON type
// contained within a [json.RawMessage], based solely on its first non-whitespace character.
// It is used by types like [Data] and [Params] to offer insight into the contained
// data without performing a full unmarshal.
//
// Note: This is only a hint and does not guarantee the [json.RawMessage] contains
// valid JSON of that type.
type TypeHint int

const (
	TypeUnknown TypeHint = iota // Could not determine type from the first character (e.g., invalid JSON start).
	TypeArray                   // Likely a JSON array (starts with '[').
	TypeObject                  // Likely a JSON object (starts with '{').
	TypeBool                    // Likely a JSON boolean (starts with 't' or 'f').
	TypeNumber                  // Likely a JSON number (starts with '-', '0'-'9').
	TypeString                  // Likely a JSON string (starts with '"').
	TypeNull                    // Likely the JSON null value (starts with 'n').

	// TypeEmpty is returned when the [json.RawMessage], after trimming whitespace,
	// has zero length.
	TypeEmpty
	// TypeNotJSON is returned by container types ([Data], [Params]) when their
	// internal value is not a [json.RawMessage], preventing a JSON type hint.
	TypeNotJSON
)

// HintType examines the first non-whitespace byte of a [json.RawMessage]
// to provide a [TypeHint] about the potential JSON data type it represents.
// It returns [TypeEmpty] if the message is empty after trimming whitespace.
// It returns [TypeUnknown] if the first character doesn't match known JSON type indicators.
//
// This function provides a fast check but does not validate the entire JSON structure.
func HintType(m json.RawMessage) TypeHint {
	// Trim leading and trailing whitespace according to JSON rules.
	m = bytes.TrimSpace(m)

	if len(m) == 0 {
		return TypeEmpty
	}

	switch m[0] {
	case '[':
		return TypeArray
	case '{':
		return TypeObject
	case 't', 'f':
		return TypeBool
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return TypeNumber
	case '"':
		return TypeString
	case 'n':
		return TypeNull
	}

	return TypeUnknown
}
