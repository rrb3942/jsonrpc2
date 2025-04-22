package jsonrpc2

import (
	"encoding/json"
)

// TypeHint provides a hint to the json data type contained in a [json.RawMessage].
type TypeHint int

const (
	TypeUnknown TypeHint = iota
	TypeArray
	TypeObj
	TypeBool
	TypeNumber
	TypeString
	TypeNull

	// Returned when the [json.RawMessage] is empty (len == 0).
	TypeEmpty
	// Returned by types that box values that may not be a [json.RawMessage].
	TypeNotJSON
)

// jsonHintType checks the first byte of a [json.RawMessage] to determine
// the contained json data type.
func jsonHintType(m json.RawMessage) TypeHint {
	if len(m) == 0 {
		return TypeEmpty
	}

	switch m[0] {
	case '[':
		return TypeArray
	case '{':
		return TypeObj
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
