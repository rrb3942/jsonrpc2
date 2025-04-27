package jsonrpc2

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRequest(t *testing.T) {
	t.Parallel()

	// Test with int64 ID
	reqInt := NewRequest(int64(1), "testMethodInt")
	assert.Equal(t, "testMethodInt", reqInt.Method)
	assert.False(t, reqInt.ID.IsZero())
	assert.False(t, reqInt.ID.IsNull())
	idInt, err := reqInt.ID.Int64()
	assert.NoError(t, err)
	assert.Equal(t, int64(1), idInt)
	assert.True(t, reqInt.Params.IsZero())

	// Test with string ID
	reqStr := NewRequest("req-id-string", "testMethodStr")
	assert.Equal(t, "testMethodStr", reqStr.Method)
	assert.False(t, reqStr.ID.IsZero())
	assert.False(t, reqStr.ID.IsNull())
	idStr, ok := reqStr.ID.String()
	assert.True(t, ok)
	assert.Equal(t, "req-id-string", idStr)
	assert.True(t, reqStr.Params.IsZero())
}

func TestNewRequestWithParams(t *testing.T) {
	t.Parallel()

	params := NewParamsJSONObject(map[string]any{"key": "value"})
	req := NewRequestWithParams(int64(2), "methodWithParams", params)

	assert.Equal(t, "methodWithParams", req.Method)
	assert.False(t, req.ID.IsZero())
	idInt, err := req.ID.Int64()
	assert.NoError(t, err)
	assert.Equal(t, int64(2), idInt)

	assert.False(t, req.Params.IsZero())

	var decodedParams map[string]any
	err = req.Params.Unmarshal(&decodedParams)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"key": "value"}, decodedParams)
}

func TestRequest_ResponseWithError(t *testing.T) {
	t.Parallel()

	req := NewRequest(int64(3), "errorMethod")

	// Test with standard error
	stdErr := errors.New("standard error occurred")
	respStdErr := req.ResponseWithError(stdErr)
	assert.Equal(t, req.ID, respStdErr.ID)
	assert.True(t, respStdErr.Result.IsZero())
	assert.False(t, respStdErr.Error.IsZero())
	assert.Equal(t, ErrInternalError.Code, respStdErr.Error.Code)
	assert.Equal(t, ErrInternalError.Message, respStdErr.Error.Message)

	gotData := respStdErr.Error.Data.Value()
	assert.Equal(t, stdErr.Error(), gotData)

	// Test with jsonrpc2.Error
	rpcErr := NewError(-32000, "RPC specific error")
	respRPCErr := req.ResponseWithError(rpcErr)
	assert.Equal(t, req.ID, respRPCErr.ID)
	assert.True(t, respRPCErr.Result.IsZero())
	assert.False(t, respRPCErr.Error.IsZero())
	assert.Equal(t, rpcErr.Code, respRPCErr.Error.Code)
	assert.Equal(t, rpcErr.Message, respRPCErr.Error.Message)
	assert.True(t, respRPCErr.Error.Data.IsZero()) // No data added in this case
}

func TestRequest_ResponseWithResult(t *testing.T) {
	t.Parallel()

	req := NewRequest("result-req", "resultMethod")
	resultData := map[string]any{"status": "success", "value": 123}
	resp := req.ResponseWithResult(resultData)

	assert.Equal(t, req.ID, resp.ID)
	assert.True(t, resp.Error.IsZero())
	assert.False(t, resp.Result.IsZero())

	gotResult := resp.Result.Value()

	assert.Equal(t, gotResult, resultData)
}

func TestRequest_IsNotification(t *testing.T) {
	t.Parallel()

	// Regular request
	req := NewRequest(int64(1), "method")
	assert.False(t, req.IsNotification())

	// Notification (created via Request struct directly with zero ID)
	notification := &Request{Method: "notifyMethod"} // ID is zero value
	assert.True(t, notification.IsNotification())

	// Notification (created via NewNotification)
	notificationProper := NewNotification("notifyMethodProper")
	assert.True(t, notificationProper.AsRequest().IsNotification())
}

func TestRequest_AsNotification(t *testing.T) {
	t.Parallel()

	// Regular request
	req := NewRequest(int64(1), "method")
	assert.Nil(t, req.AsNotification())

	// Notification (created via Request struct directly with zero ID)
	notificationReq := &Request{Method: "notifyMethod"} // ID is zero value
	notif := notificationReq.AsNotification()
	assert.NotNil(t, notif)
	assert.Equal(t, "notifyMethod", notif.Method)
	assert.True(t, notif.ID.IsZero()) // Should still be zero

	// Test nil receiver
	var nilReq *Request

	assert.Nil(t, nilReq.AsNotification())
}

func TestRequest_id(t *testing.T) {
	t.Parallel()

	// Request with ID
	reqWithID := NewRequest(int64(123), "method")
	assert.Equal(t, NewID(int64(123)), reqWithID.id())

	// Notification (no ID)
	notification := NewNotification("notify")
	id := notification.AsRequest().id()
	assert.True(t, id.IsZero())
}

func TestRequest_MarshalUnmarshalJSON(t *testing.T) {
	t.Parallel()

	//nolint:govet //Do not reorder struct
	testCases := []struct {
		name         string
		request      *Request
		expectedJSON string
	}{
		{
			name:         "Request with null ID, no params",
			request:      &Request{ID: NewNullID(), Method: "method1"},
			expectedJSON: `{"jsonrpc":"2.0","method":"method1","id":null}`,
		},
		{
			name:         "Request with int ID, no params",
			request:      NewRequest(int64(1), "method1"),
			expectedJSON: `{"jsonrpc":"2.0","method":"method1","id":1}`,
		},
		{
			name:         "Request with string ID, array params",
			request:      NewRequestWithParams("req-2", "method2", NewParamsArray([]any{float64(1), "two", true})),
			expectedJSON: `{"jsonrpc":"2.0","method":"method2","params":[1,"two",true],"id":"req-2"}`,
		},
		{
			name:         "Request with string ID, object params",
			request:      NewRequestWithParams("req-3", "method3", NewParamsObject(map[string]any{"key": "value", "num": float64(42)})),
			expectedJSON: `{"jsonrpc":"2.0","method":"method3","params":{"key":"value","num":42},"id":"req-3"}`,
		},
		{
			name:         "Notification with params",
			request:      NewNotificationWithParams("notify1", NewParamsArray([]any{"data"})).AsRequest(),
			expectedJSON: `{"jsonrpc":"2.0","method":"notify1","params":["data"]}`,
		},
		{
			name:         "Notification without params",
			request:      NewNotification("notify2").AsRequest(),
			expectedJSON: `{"jsonrpc":"2.0","method":"notify2"}`,
		},
	}

	for _, tc := range testCases {
		// Capture range variable
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Marshal
			jsonData, err := json.Marshal(tc.request)
			require.NoError(t, err, "Marshaling failed")
			assert.JSONEq(t, tc.expectedJSON, string(jsonData), "Marshaled JSON does not match expected")

			// Unmarshal
			var unmarshaledReq Request
			err = json.Unmarshal(jsonData, &unmarshaledReq)
			require.NoError(t, err, "Unmarshalling failed")

			// Need to manually set Jsonrpc version on original request for comparison
			// as it's added during marshaling but not part of the constructor.
			// Also handle the case where ID might be explicitly null vs omitted for notifications.
			expectedReq := *tc.request
			expectedReq.Jsonrpc = Version{present: true}

			if expectedReq.IsNotification() {
				// Ensure ID is truly zero for comparison after unmarshal sets it to null ID
				expectedReq.ID = ID{}
			}

			// Compare fields carefully, especially Params which might need deep comparison
			// if not using testify's Equal which handles this.
			assert.Equal(t, expectedReq.Jsonrpc, unmarshaledReq.Jsonrpc, "Jsonrpc version mismatch")
			assert.Equal(t, expectedReq.Method, unmarshaledReq.Method, "Method mismatch")

			// Zero IDs are not equal by ID.Equal, do deep equal
			if expectedReq.IsNotification() {
				assert.Equal(t, expectedReq.ID, unmarshaledReq.ID, "Zero ID mismatch")
			} else {
				// Must use ID.Equal to handle unmarshal to json.Number
				assert.True(t, expectedReq.ID.Equal(unmarshaledReq.ID), "ID mismatch")
			}

			// Compare Params
			if expectedReq.Params.IsZero() {
				assert.True(t, unmarshaledReq.Params.IsZero(), "Expected zero params, but got non-zero")
			} else {
				assert.False(t, unmarshaledReq.Params.IsZero(), "Expected non-zero params, but got zero")
				// Unmarshal original and unmarshaled params into maps/slices for comparison
				var unmarshaledParamsVal any
				err = unmarshaledReq.Params.Unmarshal(&unmarshaledParamsVal)
				require.NoError(t, err, "Failed to unmarshal actual params for comparison")
				assert.Equal(t, expectedReq.Params.value, unmarshaledParamsVal, "Params content mismatch")
			}
		})
	}
}

// Helper to create Params from an object easily for tests.
func NewParamsJSONObject(v any) Params {
	//nolint:errchkjson //Helper func for testing
	raw, _ := json.Marshal(v)
	p := Params{}
	_ = p.UnmarshalJSON(raw) // Assign value via UnmarshalJSON

	return p
}

// Helper to create Params from an array easily for tests.
func NewParamsJSONArray(v any) Params {
	//nolint:errchkjson //Helper func for testing
	raw, _ := json.Marshal(v)
	p := Params{}
	_ = p.UnmarshalJSON(raw) // Assign value via UnmarshalJSON

	return p
}
