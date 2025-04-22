package jsonrpc2

import (
	"encoding/json"
	"testing"
)

func TestJsonHintType(t *testing.T) {
	tests := []struct {
		name     string
		input    json.RawMessage
		expected TypeHint
	}{
		{"Empty", json.RawMessage{}, TypeEmpty},
		{"Array", json.RawMessage(`[]`), TypeArray},
		{"Object", json.RawMessage(`{}`), TypeObj},
		{"True", json.RawMessage(`true`), TypeBool},
		{"False", json.RawMessage(`false`), TypeBool},
		{"Positive Integer", json.RawMessage(`123`), TypeNumber},
		{"Negative Integer", json.RawMessage(`-123`), TypeNumber},
		{"Zero", json.RawMessage(`0`), TypeNumber},
		{"Float", json.RawMessage(`123.45`), TypeNumber},
		{"String", json.RawMessage(`"hello"`), TypeString},
		{"Null", json.RawMessage(`null`), TypeNull},
		{"Unknown", json.RawMessage(`invalid`), TypeUnknown},
		{"Whitespace Array", json.RawMessage(`  [ ] `), TypeUnknown}, // Whitespace is not handled, expects first char
		{"Whitespace Object", json.RawMessage(`  { } `), TypeUnknown}, // Whitespace is not handled, expects first char
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := jsonHintType(tt.input)
			if got != tt.expected {
				t.Errorf("jsonHintType(%q) = %v; want %v", tt.input, got, tt.expected)
			}
		})
	}
}
