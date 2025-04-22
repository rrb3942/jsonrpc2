package jsonrpc2

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestData_RawMessage(t *testing.T) {
	//nolint:govet //Do not reorder struct
	tests := []struct {
		name  string
		input Data
		want  json.RawMessage
	}{
		{
			name:  "with raw message",
			input: Data{value: json.RawMessage(`{"key":"value"}`)},
			want:  json.RawMessage(`{"key":"value"}`),
		},
		{
			name:  "with non-raw message",
			input: Data{value: map[string]string{"key": "value"}},
			want:  nil,
		},
		{
			name:  "with nil value",
			input: Data{value: nil},
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.input.RawMessage(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Data.RawMessage() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestData_Value(t *testing.T) {
	//nolint:govet //Do not reorder struct
	tests := []struct {
		name  string
		input Data
		want  any
	}{
		{
			name:  "raw message",
			input: Data{value: json.RawMessage(`[1, 2, 3]`)},
			want:  json.RawMessage(`[1, 2, 3]`),
		},
		{
			name:  "map",
			input: Data{value: map[string]int{"a": 1}},
			want:  map[string]int{"a": 1},
		},
		{
			name:  "nil",
			input: Data{value: nil},
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.input.Value(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Data.Value() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestData_TypeHint(t *testing.T) {
	//nolint:govet //Do not reorder struct
	tests := []struct {
		name  string
		input Data
		want  TypeHint
	}{
		{
			name:  "object",
			input: Data{value: json.RawMessage(`{"key":"value"}`)},
			want:  TypeObject,
		},
		{
			name:  "array",
			input: Data{value: json.RawMessage(`[1, 2, 3]`)},
			want:  TypeArray,
		},
		{
			name:  "empty raw message",
			input: Data{value: json.RawMessage("")},
			want:  TypeEmpty,
		},
		{
			name:  "null raw message",
			input: Data{value: json.RawMessage("null")},
			want:  TypeNull,
		},
		{
			name:  "string raw message",
			input: Data{value: json.RawMessage(`"string"`)},
			want:  TypeString,
		},
		{
			name:  "number raw message",
			input: Data{value: json.RawMessage(`123`)},
			want:  TypeNumber,
		},
		{
			name:  "bool raw message",
			input: Data{value: json.RawMessage(`true`)},
			want:  TypeBool,
		},
		{
			name:  "non-raw message",
			input: Data{value: map[string]string{"key": "value"}},
			want:  TypeNotJSON,
		},
		{
			name:  "nil value",
			input: Data{value: nil},
			want:  TypeNotJSON, // nil is not json.RawMessage
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.input.TypeHint(); got != tt.want {
				t.Errorf("Data.TypeHint() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestData_Unmarshal(t *testing.T) {
	type testStruct struct {
		Key string `json:"key"`
	}

	//nolint:govet //Do not reorder struct
	tests := []struct {
		name    string
		input   Data
		target  any
		want    any
		wantErr error
	}{
		{
			name:   "unmarshal object to struct",
			input:  Data{value: json.RawMessage(`{"key":"value"}`)},
			target: &testStruct{},
			want:   &testStruct{Key: "value"},
		},
		{
			name:   "unmarshal array to slice",
			input:  Data{value: json.RawMessage(`[1, 2, 3]`)},
			target: &[]int{},
			want:   &[]int{1, 2, 3},
		},
		{
			name:    "unmarshal from nil value",
			input:   Data{value: nil},
			target:  &testStruct{},
			want:    &testStruct{}, // Target should be untouched
			wantErr: ErrEmptyData,
		},
		{
			name:    "unmarshal from non-raw message",
			input:   Data{value: map[string]string{"key": "value"}},
			target:  &testStruct{},
			want:    &testStruct{}, // Target should be untouched
			wantErr: ErrNotRawMessage,
		},
		{
			name:    "unmarshal invalid json",
			input:   Data{value: json.RawMessage(`{invalid`)},
			target:  &testStruct{},
			want:    &testStruct{},       // Target might be partially modified or zeroed by json decoder
			wantErr: &json.SyntaxError{}, // Expecting a json error
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.input.Unmarshal(tt.target)

			if tt.wantErr != nil {
				// Check if the error type matches, ignoring the specific message for json errors
				if !errors.Is(err, tt.wantErr) && reflect.TypeOf(err) != reflect.TypeOf(tt.wantErr) {
					t.Errorf("Data.Unmarshal() error = %v, want type %T", err, tt.wantErr)
					return
				}
			} else if err != nil {
				t.Errorf("Data.Unmarshal() unexpected error = %v", err)
				return
			}

			if !reflect.DeepEqual(tt.target, tt.want) {
				t.Errorf("Data.Unmarshal() target = %v, want %v", tt.target, tt.want)
			}
		})
	}
}

func TestData_IsZero(t *testing.T) {
	//nolint:govet //Do not reorder struct
	tests := []struct {
		name  string
		input Data
		want  bool
	}{
		{
			name:  "nil value",
			input: Data{value: nil},
			want:  true,
		},
		{
			name:  "empty raw message",
			input: Data{value: json.RawMessage("")},
			want:  true,
		},
		{
			name:  "non-empty raw message",
			input: Data{value: json.RawMessage(`{}`)},
			want:  false,
		},
		{
			name:  "non-raw message value",
			input: Data{value: map[string]string{}},
			want:  false, // IsZero only checks nil or zero-length RawMessage
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.input.IsZero(); got != tt.want {
				t.Errorf("Data.IsZero() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestData_UnmarshalJSON(t *testing.T) {
	//nolint:govet //Do not reorder struct
	tests := []struct {
		name    string
		input   []byte
		want    Data
		wantErr bool
	}{
		{
			name:  "valid object",
			input: []byte(`{"key":"value"}`),
			want:  Data{value: json.RawMessage(`{"key":"value"}`)},
		},
		{
			name:  "valid array",
			input: []byte(`[1, 2, 3]`),
			want:  Data{value: json.RawMessage(`[1, 2, 3]`)},
		},
		{
			name:  "valid null",
			input: []byte(`null`),
			want:  Data{value: json.RawMessage(`null`)},
		},
		{
			name:    "invalid json",
			input:   []byte(`{invalid`),
			wantErr: true,
		},
		{
			name:    "empty input", // Considered invalid by json.Unmarshal
			input:   []byte(``),
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d Data

			err := d.UnmarshalJSON(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("Data.UnmarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && !reflect.DeepEqual(d, tt.want) {
				t.Errorf("Data.UnmarshalJSON() = %v, want %v", d, tt.want)
			}
		})
	}
}

func TestData_MarshalJSON(t *testing.T) {
	//nolint:govet //Do not reorder struct
	tests := []struct {
		name    string
		input   Data
		want    []byte
		wantErr bool
	}{
		{
			name:  "raw message object",
			input: Data{value: json.RawMessage(`{"key":"value"}`)},
			want:  []byte(`{"key":"value"}`),
		},
		{
			name:  "raw message array",
			input: Data{value: json.RawMessage(`[1, 2, 3]`)},
			want:  []byte(`[1,2,3]`),
		},
		{
			name:  "map value",
			input: Data{value: map[string]int{"a": 1}},
			want:  []byte(`{"a":1}`),
		},
		{
			name:  "struct value",
			input: Data{value: struct{ Name string }{Name: "test"}},
			want:  []byte(`{"Name":"test"}`),
		},
		{
			name:  "nil value",
			input: Data{value: nil},
			want:  []byte(`null`),
		},
		{
			name:    "unmarshalable value", // e.g., channel
			input:   Data{value: make(chan int)},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.input.MarshalJSON()
			if (err != nil) != tt.wantErr {
				t.Errorf("Data.MarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Data.MarshalJSON() = %s, want %s", got, tt.want)
			}
		})
	}
}
