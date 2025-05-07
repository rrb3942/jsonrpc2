package jsonrpc2

import (
	"encoding/json"
	"testing"
)

func TestJsonHintType(t *testing.T) {
	//nolint:govet //Do not reorder struct
	tests := []struct {
		name     string
		input    json.RawMessage
		expected TypeHint
	}{
		{"Empty", json.RawMessage{}, TypeEmpty},
		{"Array", json.RawMessage(`[]`), TypeArray},
		{"Object", json.RawMessage(`{}`), TypeObject},
		{"True", json.RawMessage(`true`), TypeBool},
		{"False", json.RawMessage(`false`), TypeBool},
		{"Positive Integer", json.RawMessage(`123`), TypeNumber},
		{"Negative Integer", json.RawMessage(`-123`), TypeNumber},
		{"Zero", json.RawMessage(`0`), TypeNumber},
		{"Float", json.RawMessage(`123.45`), TypeNumber},
		{"String", json.RawMessage(`"hello"`), TypeString},
		{"Null", json.RawMessage(`null`), TypeNull},
		{"Unknown", json.RawMessage(`invalid`), TypeUnknown},
		{"Whitespace Array", json.RawMessage(`  [ ] `), TypeArray},   // Whitespace testing
		{"Whitespace Object", json.RawMessage(`  { } `), TypeObject}, // Whitespace testing
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HintType(tt.input)
			if got != tt.expected {
				t.Errorf("jsonHintType(%q) = %v; want %v", tt.input, got, tt.expected)
			}
		})
	}
}
