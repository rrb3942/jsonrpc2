package jsonrpc2

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"testing"
)

func TestNewID(t *testing.T) {
	//nolint:govet //Dont shift order
	tests := []struct {
		name  string
		input any
		want  ID
	}{
		{
			name:  "int64",
			input: int64(123),
			want:  ID{present: true, value: int64(123)},
		},
		{
			name:  "string",
			input: "req-01",
			want:  ID{present: true, value: "req-01"},
		},
		{
			name:  "json.Number int",
			input: json.Number("456"),
			want:  ID{present: true, value: json.Number("456")},
		},
		{
			name:  "json.Number float",
			input: json.Number("78.9"),
			want:  ID{present: true, value: json.Number("78.9")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got ID
			switch v := tt.input.(type) {
			case int64:
				got = NewID(v)
			case string:
				got = NewID(v)
			case json.Number:
				got = NewID(v)
			default:
				t.Fatalf("unhandled test input type: %T", tt.input)
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewID() = %v, want %v", got, tt.want)
			}

			if got.IsZero() {
				t.Errorf("NewID().IsZero() returned true, want false")
			}
		})
	}
}

func TestNewNullID(t *testing.T) {
	got := NewNullID()
	want := ID{present: true, value: nil}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("NewNullID() = %v, want %v", got, want)
	}

	if got.IsZero() {
		t.Errorf("NewNullID().IsZero() returned true, want false")
	}

	if !got.IsNull() {
		t.Errorf("NewNullID().IsNull() returned false, want true")
	}
}

func TestID_IsZero(t *testing.T) {
	//nolint:govet //Dont shift order
	tests := []struct {
		name string
		id   ID
		want bool
	}{
		{"zero value", ID{}, true},
		{"int64", NewID(int64(1)), false},
		{"string", NewID("a"), false},
		{"number", NewID(json.Number("1.2")), false},
		{"null", NewNullID(), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.id.IsZero(); got != tt.want {
				t.Errorf("ID.IsZero() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestID_IsNull(t *testing.T) {
	//nolint:govet //Dont shift order
	tests := []struct {
		name string
		id   ID
		want bool
	}{
		{"zero value", ID{}, false}, // A zero value is not explicitly null
		{"int64", NewID(int64(1)), false},
		{"string", NewID("a"), false},
		{"number", NewID(json.Number("1.2")), false},
		{"null", NewNullID(), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.id.IsNull(); got != tt.want {
				t.Errorf("ID.IsNull() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestID_Equal(t *testing.T) {
	idInt1 := NewID(int64(1))
	idInt1Again := NewID(int64(1))
	idInt2 := NewID(int64(2))
	idStrA := NewID("a")
	idStrAAgain := NewID("a")
	idStrB := NewID("b")
	idNum1 := NewID(json.Number("1"))
	idNum1Again := NewID(json.Number("1"))
	idNum1Float := NewID(json.Number("1.0"))
	idNum2 := NewID(json.Number("2"))
	idNumFloat := NewID(json.Number("3.14"))
	idNumFloatAgain := NewID(json.Number("3.14"))
	idNull := NewNullID()
	idNullAgain := NewNullID()
	idZero := ID{}
	idZeroAgain := ID{}

	//nolint:govet //Dont shift order
	tests := []struct {
		name string
		id1  ID
		id2  ID
		want bool
	}{
		{"zero == zero", idZero, idZeroAgain, false}, // Zero IDs are never equal
		{"zero == int", idZero, idInt1, false},
		{"int == zero", idInt1, idZero, false},
		{"null == null", idNull, idNullAgain, true},
		{"null == int", idNull, idInt1, false},
		{"int == null", idInt1, idNull, false},
		{"int == int (same)", idInt1, idInt1Again, true},
		{"int == int (diff)", idInt1, idInt2, false},
		{"string == string (same)", idStrA, idStrAAgain, true},
		{"string == string (diff)", idStrA, idStrB, false},
		{"number == number (int, same)", idNum1, idNum1Again, true},
		{"number == number (int, diff)", idNum1, idNum2, false},
		{"number == number (float, same)", idNumFloat, idNumFloatAgain, true},
		{"int == number (int, same)", idInt1, idNum1, true},
		{"number == int (int, same)", idNum1, idInt1, true},
		{"int == number (int, diff)", idInt2, idNum1, false},
		{"number == int (int, diff)", idNum1, idInt2, false},
		{"int == number (float)", idInt1, idNumFloat, false},
		{"number == int (float)", idNumFloat, idInt1, false},
		{"number(int) == number(float)", idNum1, idNum1Float, false}, // String comparison for numbers
		{"string == int", idStrA, idInt1, false},
		{"int == string", idInt1, idStrA, false},
		{"string == number", idStrA, idNum1, false},
		{"number == string", idNum1, idStrA, false},
		{"string == null", idStrA, idNull, false},
		{"null == string", idNull, idStrA, false},
		{"number == null", idNum1, idNull, false},
		{"null == number", idNull, idNum1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.id1.Equal(tt.id2); got != tt.want {
				t.Errorf("ID.Equal(%v, %v) = %v, want %v", tt.id1, tt.id2, got, tt.want)
			}
			// Test symmetry
			if got := tt.id2.Equal(tt.id1); got != tt.want {
				t.Errorf("ID.Equal(%v, %v) [symmetric] = %v, want %v", tt.id2, tt.id1, got, tt.want)
			}
		})
	}
}

func TestID_Value(t *testing.T) {
	//nolint:govet //Dont shift order
	tests := []struct {
		name string
		id   ID
		want any
	}{
		{"zero", ID{}, nil},
		{"null", NewNullID(), nil},
		{"int", NewID(int64(5)), int64(5)},
		{"string", NewID("hello"), "hello"},
		{"number", NewID(json.Number("99.9")), json.Number("99.9")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.id.Value(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ID.Value() = %v (%T), want %v (%T)", got, got, tt.want, tt.want)
			}
		})
	}
}

func TestID_String(t *testing.T) {
	//nolint:govet //Dont shift order
	tests := []struct {
		name    string
		id      ID
		wantStr string
		wantOK  bool
	}{
		{"zero", ID{}, "", false},
		{"null", NewNullID(), "", false},
		{"int", NewID(int64(5)), "", false},
		{"string", NewID("hello"), "hello", true},
		{"number", NewID(json.Number("99.9")), "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStr, gotOK := tt.id.String()
			if gotStr != tt.wantStr {
				t.Errorf("ID.String() gotStr = %v, want %v", gotStr, tt.wantStr)
			}

			if gotOK != tt.wantOK {
				t.Errorf("ID.String() gotOK = %v, want %v", gotOK, tt.wantOK)
			}
		})
	}
}

func TestID_Number(t *testing.T) {
	//nolint:govet //Dont shift order
	tests := []struct {
		name    string
		id      ID
		wantNum json.Number
		wantOK  bool
	}{
		{"zero", ID{}, "", false},
		{"null", NewNullID(), "", false},
		{"int", NewID(int64(5)), "", false},
		{"string", NewID("hello"), "", false},
		{"number int", NewID(json.Number("123")), json.Number("123"), true},
		{"number float", NewID(json.Number("99.9")), json.Number("99.9"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotNum, gotOK := tt.id.Number()
			if gotNum != tt.wantNum {
				t.Errorf("ID.Number() gotNum = %v, want %v", gotNum, tt.wantNum)
			}

			if gotOK != tt.wantOK {
				t.Errorf("ID.Number() gotOK = %v, want %v", gotOK, tt.wantOK)
			}
		})
	}
}

func TestID_Int64(t *testing.T) {
	//nolint:govet //Dont shift order
	tests := []struct {
		name    string
		id      ID
		wantI64 int64
		wantErr error
	}{
		{"zero", ID{}, 0, ErrIDNotANumber},
		{"null", NewNullID(), 0, ErrIDNotANumber},
		{"int", NewID(int64(5)), 5, nil},
		{"string", NewID("hello"), 0, ErrIDNotANumber},
		{"number int", NewID(json.Number("123")), 123, nil},
		{"number float", NewID(json.Number("99.9")), 0, strconv.ErrSyntax}, // Specific error from json.Number.Int64()
		{"number large", NewID(json.Number("9223372036854775808")), 9223372036854775807, strconv.ErrRange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotI64, gotErr := tt.id.Int64()
			if gotI64 != tt.wantI64 {
				t.Errorf("ID.Int64() gotI64 = %v, want %v", gotI64, tt.wantI64)
			}

			// Got an error and want an error, compare them
			if gotErr != nil && tt.wantErr != nil {
				// Make sure errors match
				if !errors.Is(gotErr, tt.wantErr) {
					t.Errorf("ID.Int64() error type mismatch: got %v, want %v", gotErr, tt.wantErr)
				}
				// Mismatch between getting and error and wanting an error
			} else if (gotErr == nil && tt.wantErr != nil) || (gotErr != nil && tt.wantErr == nil) {
				t.Errorf("ID.Int64() error mismatch: got %v, want %v", gotErr, tt.wantErr)
			}
		})
	}
}

func TestID_MarshalJSON(t *testing.T) {
	//nolint:govet //Dont shift order
	tests := []struct {
		name    string
		id      ID
		want    string
		wantErr bool
	}{
		{"zero", ID{}, "null", false}, // Marshaling a zero ID results in null
		{"null", NewNullID(), "null", false},
		{"int", NewID(int64(123)), "123", false},
		{"string", NewID("abc"), `"abc"`, false},
		{"number int", NewID(json.Number("456")), "456", false},
		{"number float", NewID(json.Number("78.9")), "78.9", false},
		// Add a test case for a type that cannot be marshaled by standard json
		// {"unmarshalable", ID{present: true, value: make(chan int)}, "", true}, // This would panic in Marshal
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(&tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("ID.MarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && string(got) != tt.want {
				t.Errorf("ID.MarshalJSON() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestID_UnmarshalJSON(t *testing.T) {
	//nolint:govet //Dont shift order
	tests := []struct {
		name    string
		data    string
		want    ID
		wantErr bool
	}{
		{"int", `123`, ID{present: true, value: json.Number("123")}, false},
		{"float", `45.67`, ID{present: true, value: json.Number("45.67")}, false},
		{"string", `"hello"`, ID{present: true, value: "hello"}, false},
		{"null", `null`, ID{present: true, value: nil}, false},
		{"empty string", `""`, ID{present: true, value: ""}, false}, // Valid string ID
		{"invalid json", `{`, ID{}, true},
		{"invalid string", `"abc`, ID{}, true},
		{"invalid number", `123.`, ID{}, true},
		{"bool true", `true`, ID{}, true}, // Bools are not valid IDs
		{"bool false", `false`, ID{}, true},
		{"object", `{"a":1}`, ID{}, true}, // Objects are not valid IDs
		{"array", `[1]`, ID{}, true},      // Arrays are not valid IDs
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got ID

			err := json.Unmarshal([]byte(tt.data), &got)
			if (err != nil) != tt.wantErr {
				t.Errorf("ID.UnmarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ID.UnmarshalJSON() = %v (%T), want %v (%T)", got, got.value, tt.want, tt.want.value)
			}
		})
	}
}
