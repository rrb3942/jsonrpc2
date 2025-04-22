package jsonrpc2

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestNewError(t *testing.T) {
	code := int64(-32000)
	msg := "Server error"
	err := NewError(code, msg)

	if !err.present {
		t.Error("Expected error to be present")
	}

	if err.err.Code != code {
		t.Errorf("Expected code %d, got %d", code, err.err.Code)
	}

	if err.err.Message != msg {
		t.Errorf("Expected message %q, got %q", msg, err.err.Message)
	}

	if !err.err.Data.IsZero() {
		t.Errorf("Expected data to be zero, but got %v", err.err.Data)
	}
}

func TestNewErrorWithData(t *testing.T) {
	code := int64(-32001)
	msg := "Custom error"
	data := map[string]string{"details": "something went wrong"}
	err := NewErrorWithData(code, msg, data)

	if !err.present {
		t.Error("Expected error to be present")
	}

	if err.err.Code != code {
		t.Errorf("Expected code %d, got %d", code, err.err.Code)
	}

	if err.err.Message != msg {
		t.Errorf("Expected message %q, got %q", msg, err.err.Message)
	}

	gotData := err.Data().Value()
	if !reflect.DeepEqual(gotData, data) {
		t.Errorf("Expected data %v, got %v", data, gotData)
	}
}

func TestAsError(t *testing.T) {
	//nolint:govet //Do not reorder struct
	tests := []struct {
		name     string
		inputErr error
		wantCode int64
		wantMsg  string
		wantData string
	}{
		{
			name:     "Standard Error",
			inputErr: errors.New("a standard error"),
			wantCode: ErrInternalError.Code(),
			wantMsg:  ErrInternalError.Message(),
			wantData: "a standard error",
		},
		{
			name:     "JSONRPC2 Error",
			inputErr: ErrMethodNotFound,
			wantCode: ErrMethodNotFound.Code(),
			wantMsg:  ErrMethodNotFound.Message(),
			wantData: "", // No data expected
		},
		{
			name:     "JSONRPC2 Error with Data",
			inputErr: ErrInvalidParams.WithData("param 'x' missing"),
			wantCode: ErrInvalidParams.Code(),
			wantMsg:  ErrInvalidParams.Message(),
			wantData: "param 'x' missing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotErr := asError(tt.inputErr)

			if gotErr.Code() != tt.wantCode {
				t.Errorf("asError(%v) code = %d; want %d", tt.inputErr, gotErr.Code(), tt.wantCode)
			}

			if gotErr.Message() != tt.wantMsg {
				t.Errorf("asError(%v) message = %q; want %q", tt.inputErr, gotErr.Message(), tt.wantMsg)
			}

			if tt.wantData != "" {
				gotData := gotErr.Data().Value()
				if !reflect.DeepEqual(gotData, tt.wantData) {
					t.Errorf("asError(%v) data = %q; want %q", tt.inputErr, gotData, tt.wantData)
				}
			} else if !gotErr.Data().IsZero() {
				t.Errorf("asError(%v) expected zero data, got %v", tt.inputErr, gotErr.Data())
			}
		})
	}
}

func TestErrorCode(t *testing.T) {
	err := NewError(123, "test")
	if code := err.Code(); code != 123 {
		t.Errorf("Expected code 123, got %d", code)
	}
}

func TestErrorMessage(t *testing.T) {
	err := NewError(123, "test message")
	if msg := err.Message(); msg != "test message" {
		t.Errorf("Expected message 'test message', got %q", msg)
	}
}

func TestErrorData(t *testing.T) {
	data := "some data"
	err := NewErrorWithData(123, "test", data)
	errData := err.Data()

	if errData.IsZero() {
		t.Fatal("Expected data to be non-zero")
	}

	gotData := errData.Value()
	if !reflect.DeepEqual(gotData, data) {
		t.Errorf("Expected data %q, got %q", data, gotData)
	}
}

func TestErrorWithData(t *testing.T) {
	originalErr := NewError(404, "Not Found")
	data := map[string]int{"line": 10}
	errWithData := originalErr.WithData(data)

	// Check original error is unchanged
	if !originalErr.Data().IsZero() {
		t.Errorf("Original error data should be zero, got %v", originalErr.Data())
	}

	// Check new error
	if errWithData.Code() != originalErr.Code() {
		t.Errorf("Expected code %d, got %d", originalErr.Code(), errWithData.Code())
	}

	if errWithData.Message() != originalErr.Message() {
		t.Errorf("Expected message %q, got %q", originalErr.Message(), errWithData.Message())
	}

	gotData := errWithData.Data().Value()
	if !reflect.DeepEqual(gotData, data) {
		t.Errorf("Expected data %v, got %v", data, gotData)
	}
}

func TestErrorIs(t *testing.T) {
	err1 := NewError(100, "Error 100")
	err2 := NewError(100, "Another Error 100")
	err3 := NewError(200, "Error 200")
	stdErr := errors.New("standard error")

	if !errors.Is(err1, &Error{err: RPCError{Code: 100}}) {
		t.Errorf("Expected err1 to be Error{Code: 100}")
	}

	if !errors.Is(err2, &Error{err: RPCError{Code: 100}}) {
		t.Errorf("Expected err2 to be Error{Code: 100}")
	}

	if errors.Is(err1, Error{err: RPCError{Code: 200}}) {
		t.Errorf("Expected err1 not to be Error{Code: 200}")
	}

	if errors.Is(err3, Error{err: RPCError{Code: 100}}) {
		t.Errorf("Expected err3 not to be Error{Code: 100}")
	}

	if errors.Is(err1, stdErr) {
		t.Errorf("Expected err1 not to be a standard error")
	}

	if !errors.Is(err1, err2) {
		t.Errorf("Expected err1 Is err2 to be true (same code)")
	}

	if errors.Is(err1, err3) {
		t.Errorf("Expected err1 Is err3 to be false (different code)")
	}
}

func TestErrorIsZero(t *testing.T) {
	var zeroErr Error

	nonZeroErr := NewError(1, "test")

	if !zeroErr.IsZero() {
		t.Error("Expected zero Error to be zero")
	}

	if nonZeroErr.IsZero() {
		t.Error("Expected non-zero Error not to be zero")
	}
}

func TestErrorError(t *testing.T) {
	msg := "This is the error message"
	err := NewError(500, msg)

	if err.Error() != msg {
		t.Errorf("Expected Error() to return %q, got %q", msg, err.Error())
	}
}

func TestErrorMarshalUnmarshalJSON(t *testing.T) {
	//nolint:govet //Do not reorder struct
	tests := []struct {
		name     string
		input    Error
		wantJSON string
	}{
		{
			name:     "Simple Error",
			input:    NewError(-32600, "Invalid Request"),
			wantJSON: `{"message":"Invalid Request","code":-32600}`,
		},
		{
			name:     "Error with String Data",
			input:    NewErrorWithData(-32602, "Invalid params", "param 'x' is missing"),
			wantJSON: `{"data":"param 'x' is missing","message":"Invalid params","code":-32602}`,
		},
		{
			name:  "Error with Object Data",
			input: NewErrorWithData(-32000, "Server error", map[string]any{"details": "DB connection failed", "retryable": false}),
			// Note: map key order isn't guaranteed in JSON
			// We'll handle this by unmarshalling the expected string too
		},
		{
			name:     "Error with Null Data",
			input:    NewErrorWithData(-32001, "Custom", nil),
			wantJSON: `{"data":null,"message":"Custom","code":-32001}`,
		},
		{
			name: "Zero Error (should not marshal)",
			// A zero error shouldn't really be marshalled on its own,
			// but testing MarshalJSON directly. It marshals to the zero RPCError.
			input:    Error{},
			wantJSON: `{"message":"","code":0}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test MarshalJSON
			jsonData, err := json.Marshal(&tt.input)
			if err != nil {
				t.Fatalf("MarshalJSON failed: %v", err)
			}

			// Unmarshal both results to compare them structurally, avoiding key order issues
			var gotMap, wantMap map[string]interface{}
			if err := json.Unmarshal(jsonData, &gotMap); err != nil {
				t.Fatalf("Failed to unmarshal actual JSON result: %v\nJSON: %s", err, string(jsonData))
			}

			// Only construct wantMap if wantJSON is provided (handles object data case)
			if tt.wantJSON != "" {
				if err := json.Unmarshal([]byte(tt.wantJSON), &wantMap); err != nil {
					t.Fatalf("Failed to unmarshal expected JSON: %v\nJSON: %s", err, tt.wantJSON)
				}

				if !reflect.DeepEqual(gotMap, wantMap) {
					t.Errorf("MarshalJSON() got = %s, want %s", string(jsonData), tt.wantJSON)
				}
			} else if tt.name == "Error with Object Data" {
				// Specific check for the object data case
				expectedCode := float64(-32000) // JSON numbers are float64 by default
				expectedMsg := "Server error"
				expectedData := map[string]interface{}{"details": "DB connection failed", "retryable": false}

				if code, ok := gotMap["code"].(float64); !ok || code != expectedCode {
					t.Errorf("Expected code %v, got %v", expectedCode, gotMap["code"])
				}

				if msg, ok := gotMap["message"].(string); !ok || msg != expectedMsg {
					t.Errorf("Expected message %q, got %q", expectedMsg, gotMap["message"])
				}

				if data, ok := gotMap["data"].(map[string]interface{}); !ok || !reflect.DeepEqual(data, expectedData) {
					t.Errorf("Expected data %v, got %v", expectedData, gotMap["data"])
				}
			}

			// Test UnmarshalJSON
			var unmarshaledErr Error
			// Use the marshalled data if wantJSON was precise, otherwise use wantJSON
			dataToUnmarshal := jsonData
			if tt.wantJSON != "" {
				dataToUnmarshal = []byte(tt.wantJSON)
			}

			if err := json.Unmarshal(dataToUnmarshal, &unmarshaledErr); err != nil {
				t.Fatalf("UnmarshalJSON failed: %v\nJSON: %s", err, string(dataToUnmarshal))
			}

			// Compare the unmarshalled error with the original input error
			// Need to compare fields directly as Data might be json.RawMessage vs original type
			if unmarshaledErr.Code() != tt.input.Code() {
				t.Errorf("UnmarshalJSON code mismatch: got %d, want %d", unmarshaledErr.Code(), tt.input.Code())
			}

			if unmarshaledErr.Message() != tt.input.Message() {
				t.Errorf("UnmarshalJSON message mismatch: got %q, want %q", unmarshaledErr.Message(), tt.input.Message())
			}

			if tt.input.Data().IsZero() {
				if !unmarshaledErr.Data().IsZero() {
					t.Errorf("UnmarshalJSON data mismatch: expected zero data, got non-zero")
				}
			} else {
				// Unmarshal both data fields into interfaces for comparison
				var gotData any
				if err := unmarshaledErr.Data().Unmarshal(&gotData); err != nil && !errors.Is(err, ErrEmptyData) { // Allow empty data for zero error case
					t.Fatalf("Failed to unmarshal 'got' data: %v", err)
				}

				wantData := tt.input.Data().Value()

				// Handle json.Number comparison if necessary
				gotData = normalizeJSONNumbers(gotData)
				wantData = normalizeJSONNumbers(wantData)

				if !reflect.DeepEqual(gotData, wantData) {
					t.Errorf("UnmarshalJSON data mismatch: got %v (%T), want %v (%T)", gotData, gotData, wantData, wantData)
				}
			}
		})
	}
}

// normalizeJSONNumbers converts json.Number instances within nested structures to int64 or float64.
func normalizeJSONNumbers(data any) any {
	switch v := data.(type) {
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i
		}

		if f, err := v.Float64(); err == nil {
			return f
		}

		return v.String() // Fallback to string if not int or float
	case map[string]any:
		normalizedMap := make(map[string]any, len(v))
		for key, val := range v {
			normalizedMap[key] = normalizeJSONNumbers(val)
		}

		return normalizedMap
	case []any:
		normalizedSlice := make([]any, len(v))
		for i, val := range v {
			normalizedSlice[i] = normalizeJSONNumbers(val)
		}

		return normalizedSlice
	default:
		return data
	}
}

func ExampleError_Is() {
	err := NewError(-32601, "Method not found")

	if errors.Is(err, ErrMethodNotFound) {
		fmt.Println("Error is Method not found.")
	}

	if !errors.Is(err, ErrInvalidRequest) {
		fmt.Println("Error is not Invalid request.")
	}

	// Output:
	// Error is Method not found.
	// Error is not Invalid request.
}

func ExampleError_WithData() {
	baseErr := NewError(-32602, "Invalid params")
	errWithDetails := baseErr.WithData("Parameter 'count' must be positive")

	fmt.Printf("Code: %d\n", errWithDetails.Code())
	fmt.Printf("Message: %s\n", errWithDetails.Message())
	fmt.Printf("Data: %s\n", errWithDetails.Data().Value())

	// Output:
	// Code: -32602
	// Message: Invalid params
	// Data: Parameter 'count' must be positive
}
