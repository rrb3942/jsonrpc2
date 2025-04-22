package jsonrpc2

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestNewParamsArray(t *testing.T) {
	t.Parallel()

	arr := []int{1, 2, 3}
	params := NewParamsArray(arr)

	if params.value == nil {
		t.Fatal("NewParamsArray resulted in nil value")
	}

	if !reflect.DeepEqual(params.value, arr) {
		t.Errorf("Expected value %v, got %v", arr, params.value)
	}
}

func TestNewParamsObj(t *testing.T) {
	t.Parallel()

	obj := map[string]string{"key": "value"}
	params := NewParamsObj(obj)

	if params.value == nil {
		t.Fatal("NewParamsObj resulted in nil value")
	}

	if !reflect.DeepEqual(params.value, obj) {
		t.Errorf("Expected value %v, got %v", obj, params.value)
	}
}

func TestNewParamsRaw(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`[1, "test"]`)
	params := NewParamsRaw(raw)

	// Note: NewParamsRaw currently calls NewParamsArray, so the internal value
	// will be the raw message itself, not wrapped in another layer.
	// This might be unexpected based on the name, but we test the current behavior.
	if params.value == nil {
		t.Fatal("NewParamsRaw resulted in nil value")
	}

	// Check if the underlying value is indeed the raw message
	if !reflect.DeepEqual(params.value, raw) {
		t.Errorf("Expected value %v, got %v", raw, params.value)
	}

	// Additionally, test RawMessage method for consistency
	retrievedRaw := params.RawMessage()
	if retrievedRaw == nil {
		t.Error("RawMessage() returned nil unexpectedly")
	} else if string(retrievedRaw) != string(raw) {
		t.Errorf("RawMessage() expected %s, got %s", string(raw), string(retrievedRaw))
	}
}

func TestParams_RawMessage(t *testing.T) {
	t.Parallel()
	//nolint:govet //Do not reorder struct
	tests := []struct {
		name   string
		params Params
		want   json.RawMessage
	}{
		{"With RawMessage", Params{value: json.RawMessage(`{"a":1}`)}, json.RawMessage(`{"a":1}`)},
		{"With Non-RawMessage", Params{value: []int{1, 2}}, nil},
		{"With Nil Value", Params{value: nil}, nil},
		{"Zero Value", Params{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.params.RawMessage(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Params.RawMessage() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParams_TypeHint(t *testing.T) {
	t.Parallel()
	//nolint:govet //Do not reorder struct
	tests := []struct {
		name   string
		params Params
		want   TypeHint
	}{
		{"Array", Params{value: json.RawMessage(`[1, 2]`)}, TypeArray},
		{"Object", Params{value: json.RawMessage(`{"a":1}`)}, TypeObject},
		{"Empty Raw", Params{value: json.RawMessage(``)}, TypeEmpty},
		{"Whitespace Raw", Params{value: json.RawMessage(`  `)}, TypeEmpty},
		{"Non-RawMessage", Params{value: []int{1, 2}}, TypeNotJSON},
		{"Nil Value", Params{value: nil}, TypeNotJSON},
		{"Zero Value", Params{}, TypeNotJSON},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.params.TypeHint(); got != tt.want {
				t.Errorf("Params.TypeHint() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParams_Unmarshal(t *testing.T) {
	t.Parallel()

	type testStruct struct {
		B string `json:"b"`
		A int    `json:"a"`
	}

	//nolint:govet //Do not reorder struct
	tests := []struct {
		name    string
		params  Params
		target  any
		want    any
		wantErr error
	}{
		{
			name:   "Unmarshal Object",
			params: Params{value: json.RawMessage(`{"a":1, "b":"hello"}`)},
			target: &testStruct{},
			want:   &testStruct{A: 1, B: "hello"},
		},
		{
			name:   "Unmarshal Array",
			params: Params{value: json.RawMessage(`[1, 2, 3]`)},
			target: &[]int{},
			want:   &[]int{1, 2, 3},
		},
		{
			name:    "Unmarshal Wrong Type",
			params:  Params{value: json.RawMessage(`{"a": "not a number"}`)},
			target:  &testStruct{},
			wantErr: &json.UnmarshalTypeError{}, // Underlying json.Unmarshal error
		},
		{
			name:    "Unmarshal Invalid JSON",
			params:  Params{value: json.RawMessage(`{invalid`)},
			target:  &testStruct{},
			wantErr: &json.SyntaxError{}, // Underlying json.Unmarshal error
		},
		{
			name:    "Not RawMessage",
			params:  Params{value: []int{1, 2}},
			target:  &[]int{},
			wantErr: ErrNotRawMessage,
		},
		{
			name:    "Nil Value",
			params:  Params{value: nil},
			target:  &[]int{},
			wantErr: ErrNotRawMessage,
		},
		{
			name:    "Zero Value",
			params:  Params{},
			target:  &[]int{},
			wantErr: ErrNotRawMessage,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.params.Unmarshal(tt.target)

			//nolint:nestif //Testing logic can get complicated
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("Params.Unmarshal() error = %v, wantErr %v", err, tt.wantErr)
				}

				if !errors.Is(err, tt.wantErr) {
					// json.SyntaxError is annoying to test
					synErr := &json.SyntaxError{}
					syntaxError := &json.SyntaxError{}

					if errors.As(tt.wantErr, &syntaxError) {
						if errors.As(err, &synErr) {
							return
						}
					}

					// json.UnmarshalTypeError is annoying to test
					marErr := &json.UnmarshalTypeError{}
					unmarshalTypeError := &json.UnmarshalTypeError{}

					if errors.As(tt.wantErr, &unmarshalTypeError) {
						if errors.As(err, &marErr) {
							return
						}
					}

					t.Fatalf("Params.Unmarshal() error = %v, wantErr %v", err, tt.wantErr)
				}
			} else {
				if err != nil {
					t.Fatalf("Params.Unmarshal() unexpected error = %v", err)
				}

				if !reflect.DeepEqual(tt.target, tt.want) {
					t.Errorf("Params.Unmarshal() got = %v, want %v", tt.target, tt.want)
				}
			}
		})
	}
}

func TestParams_IsZero(t *testing.T) {
	t.Parallel()
	//nolint:govet //Do not reorder struct
	tests := []struct {
		name   string
		params Params
		want   bool
	}{
		{"Zero Value", Params{}, true},
		{"Nil Value", Params{value: nil}, true},
		{"Empty RawMessage", Params{value: json.RawMessage("")}, true},
		{"Whitespace RawMessage", Params{value: json.RawMessage("   ")}, false}, // Whitespace is not zero length
		{"Non-Empty RawMessage", Params{value: json.RawMessage(`[]`)}, false},
		{"Non-RawMessage Value", Params{value: []int{}}, false}, // Non-raw is never zero by this definition
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.params.IsZero(); got != tt.want {
				t.Errorf("Params.IsZero() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParams_UnmarshalJSON(t *testing.T) {
	t.Parallel()
	//nolint:govet //Do not reorder struct
	tests := []struct {
		name    string
		data    []byte
		want    Params
		wantErr error
	}{
		{"Valid Object", []byte(`{"key":"value"}`), Params{value: json.RawMessage(`{"key":"value"}`)}, nil},
		{"Valid Array", []byte(`[1, 2, 3]`), Params{value: json.RawMessage(`[1, 2, 3]`)}, nil},
		{"Empty Object", []byte(`{}`), Params{value: json.RawMessage(`{}`)}, nil},
		{"Empty Array", []byte(`[]`), Params{value: json.RawMessage(`[]`)}, nil},
		{"Invalid JSON", []byte(`{invalid`), Params{}, &json.SyntaxError{}}, // Invalid json never makes to internal unmarshal
		{"Not Object or Array (String)", []byte(`"string"`), Params{}, errInvalidParamDecode},
		{"Not Object or Array (Number)", []byte(`123`), Params{}, errInvalidParamDecode},
		{"Not Object or Array (Null)", []byte(`null`), Params{}, errInvalidParamDecode},
		{"Not Object or Array (Bool)", []byte(`true`), Params{}, errInvalidParamDecode},
		{"Empty Data", []byte(``), Params{}, &json.SyntaxError{}}, // Invalid json never makes it to internal unmarshal
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var p Params
			err := json.Unmarshal(tt.data, &p)

			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("UnmarshalJSON error = %v, wantErr %v", err, tt.wantErr)
				}

				if !errors.Is(err, tt.wantErr) {
					// json.SyntaxError is annoying to test
					synErr := &json.SyntaxError{}
					syntaxError := &json.SyntaxError{}

					if errors.As(tt.wantErr, &syntaxError) {
						if errors.As(err, &synErr) {
							return
						}
					}

					t.Fatalf("UnmarshalJSON error type = %T, wantErr type %T (%v vs %v)", err, tt.wantErr, err, tt.wantErr)
				}
			} else {
				if err != nil {
					t.Fatalf("UnmarshalJSON unexpected error = %v", err)
				}
				// Compare internal value directly for simplicity
				gotRaw, gotOk := p.value.(json.RawMessage)
				wantRaw, wantOk := tt.want.value.(json.RawMessage)

				if gotOk != wantOk || string(gotRaw) != string(wantRaw) {
					t.Errorf("UnmarshalJSON got = %v, want %v", p, tt.want)
				}
			}
		})
	}
}

func TestParams_MarshalJSON(t *testing.T) {
	t.Parallel()
	//nolint:govet //Do not reorder struct
	tests := []struct {
		name    string
		params  Params
		want    []byte
		wantErr bool
	}{
		{"From Raw Object", Params{value: json.RawMessage(`{"a":1}`)}, []byte(`{"a":1}`), false},
		{"From Raw Array", Params{value: json.RawMessage(`[1,2]`)}, []byte(`[1,2]`), false},
		{"From Go Slice", NewParamsArray([]int{1, 2, 3}), []byte(`[1,2,3]`), false},
		{"From Go Map", NewParamsObj(map[string]int{"a": 1}), []byte(`{"a":1}`), false},
		{"Nil Value", Params{value: nil}, []byte(`null`), false},
		{"Zero Value", Params{}, []byte(`null`), false},
		// Add a test case that might cause marshaling error if needed, e.g., channel
		{"Unsupported Type", Params{value: make(chan int)}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(&tt.params)
			if (err != nil) != tt.wantErr {
				t.Errorf("MarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MarshalJSON() got = %s, want %s", got, tt.want)
			}
		})
	}
}
