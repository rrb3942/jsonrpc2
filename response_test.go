package jsonrpc2

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewResponseWithResult(t *testing.T) {
	t.Parallel()

	//nolint:govet //Do not reorder struct
	testCases := []struct {
		name     string
		id       any
		result   any
		expected *Response
	}{
		{
			name:   "int id, string result",
			id:     int64(1),
			result: "success",
			expected: &Response{
				ID:     NewID(int64(1)),
				Result: NewResult("success"),
			},
		},
		{
			name:   "string id, int result",
			id:     "req-abc",
			result: 123,
			expected: &Response{
				ID:     NewID("req-abc"),
				Result: NewResult(123),
			},
		},
		{
			name:   "int id, struct result",
			id:     int64(2),
			result: struct{ Value int }{Value: 42},
			expected: &Response{
				ID:     NewID(int64(2)),
				Result: NewResult(struct{ Value int }{Value: 42}),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var resp *Response
			switch id := tc.id.(type) {
			case int64:
				resp = NewResponseWithResult(id, tc.result)
			case string:
				resp = NewResponseWithResult(id, tc.result)
			default:
				t.Fatalf("unsupported id type: %T", tc.id)
			}

			assert.Equal(t, tc.expected.ID, resp.ID, "ID mismatch")
			assert.Equal(t, tc.expected.Result, resp.Result, "Result mismatch")
			assert.True(t, resp.Error.IsZero(), "Error should be zero")
			assert.False(t, resp.IsError(), "IsError should be false")
		})
	}
}

func TestResponse_Unmarshal(t *testing.T) {
	t.Parallel()

	type myStruct struct {
		Field string `json:"field"`
	}

	t.Run("unmarshal result success", func(t *testing.T) {
		t.Parallel()

		resultData := myStruct{Field: "test_data"}
		rawResult, err := json.Marshal(resultData)
		require.NoError(t, err, "Failed to marshal test data for result")

		resp := &Response{
			ID:     NewID(int64(1)),
			Result: Result{present: true, value: json.RawMessage(rawResult)},
			Error:  Error{}, // Ensure Error is zero
		}

		var target myStruct
		err = resp.Unmarshal(&target)
		require.NoError(t, err)
		assert.Equal(t, "test_data", target.Field)
	})

	t.Run("unmarshal result when response is error", func(t *testing.T) {
		t.Parallel()

		resp := NewResponseWithError(int64(1), NewError(123, "test error"))

		var target myStruct
		err := resp.Unmarshal(&target)
		require.Error(t, err)
		assert.Equal(t, ErrResponseIsError, err)
	})

	t.Run("unmarshal result with nil value", func(t *testing.T) {
		t.Parallel()
		// Create a response where Result.value is json.RawMessage("null")
		// This simulates a JSON `null` result.
		resp := &Response{
			ID:     NewID(int64(1)),
			Result: Result{present: true, value: json.RawMessage("null")},
			Error:  Error{}, // Ensure Error is zero
		}

		var target *myStruct // Target must be a pointer to handle JSON null
		err := resp.Unmarshal(&target)
		require.NoError(t, err)
		assert.Nil(t, target)
	})

	t.Run("unmarshal result with zero value Result struct", func(t *testing.T) {
		t.Parallel()
		// This represents a response where the "result" field was entirely absent
		// or Result was not properly initialized.
		resp := &Response{ID: NewID(int64(1)), Result: Result{}} // Result{}.IsZero() is true

		var target myStruct
		err := resp.Unmarshal(&target)
		// Result.Unmarshal returns ErrEmptyData when its internal value is not json.RawMessage
		// or if it's nil (which is the case for a zero Result struct).
		require.ErrorIs(t, err, ErrEmptyData)
	})
}

func TestResponse_UnmarshalError(t *testing.T) {
	t.Parallel()

	type myErrorData struct {
		Detail string `json:"detail"`
	}

	t.Run("unmarshal error data success", func(t *testing.T) {
		t.Parallel()

		errorPayload := myErrorData{Detail: "error_detail"}
		rawErrorPayload, err := json.Marshal(errorPayload)
		require.NoError(t, err, "Failed to marshal test data for error data")

		respError := NewError(123, "test error")
		respError.Data = ErrorData{present: true, value: json.RawMessage(rawErrorPayload)}

		resp := &Response{
			ID:     NewID(int64(1)),
			Error:  respError,
			Result: Result{}, // Ensure Result is zero
		}

		var target myErrorData
		err = resp.UnmarshalError(&target)
		require.NoError(t, err)
		assert.Equal(t, "error_detail", target.Detail)
	})

	t.Run("unmarshal error data when response is not error", func(t *testing.T) {
		t.Parallel()

		resp := NewResponseWithResult(int64(1), "success")

		var target myErrorData
		err := resp.UnmarshalError(&target)
		require.Error(t, err)
		assert.Equal(t, ErrResponseNotError, err)
	})

	t.Run("unmarshal error data when error data is nil", func(t *testing.T) {
		t.Parallel()
		// Error without explicit data, Error.Data will be zero.
		resp := NewResponseWithError(int64(1), NewError(123, "test error"))

		var target *myErrorData // Target must be a pointer to handle potential JSON null or empty data
		err := resp.UnmarshalError(&target)
		// Error.Data.Unmarshal returns ErrEmptyData when its internal value is not json.RawMessage
		// or if it's nil (which is the case for a zero ErrorData struct).
		require.ErrorIs(t, err, ErrEmptyData)
		assert.Nil(t, target) // Expect target to be nil as no data was unmarshalled
	})

	t.Run("unmarshal error data with zero value Error struct", func(t *testing.T) {
		t.Parallel()
		// This represents a response where the "error" field was entirely absent
		// or Error was not properly initialized.
		resp := &Response{ID: NewID(int64(1)), Error: Error{}} // Error{}.IsZero() is true

		var target myErrorData
		err := resp.UnmarshalError(&target)
		require.ErrorIs(t, err, ErrResponseNotError) // IsZero() makes it seem like not an error
	})
}

func TestNewResponseWithError(t *testing.T) {
	t.Parallel()

	customErr := NewError(-32000, "Custom error")
	stdErr := errors.New("standard error")
	internalErrWithData := ErrInternalError.WithData(stdErr.Error())

	//nolint:govet //Do not reorder struct
	testCases := []struct {
		name     string
		id       any
		err      error
		expected *Response
	}{
		{
			name: "int id, jsonrpc2 error",
			id:   int64(1),
			err:  customErr,
			expected: &Response{
				ID:    NewID(int64(1)),
				Error: customErr,
			},
		},
		{
			name: "string id, jsonrpc2 error",
			id:   "req-xyz",
			err:  customErr,
			expected: &Response{
				ID:    NewID("req-xyz"),
				Error: customErr,
			},
		},
		{
			name: "int id, standard error",
			id:   int64(2),
			err:  stdErr,
			expected: &Response{
				ID:    NewID(int64(2)),
				Error: internalErrWithData, // Expect it to be wrapped
			},
		},
		{
			name: "string id, standard error",
			id:   "req-123",
			err:  stdErr,
			expected: &Response{
				ID:    NewID("req-123"),
				Error: internalErrWithData, // Expect it to be wrapped
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var resp *Response
			switch id := tc.id.(type) {
			case int64:
				resp = NewResponseWithError(id, tc.err)
			case string:
				resp = NewResponseWithError(id, tc.err)
			default:
				t.Fatalf("unsupported id type: %T", tc.id)
			}

			assert.Equal(t, tc.expected.ID, resp.ID, "ID mismatch")
			assert.Equal(t, tc.expected.Error, resp.Error, "Error mismatch")
			assert.True(t, resp.Result.IsZero(), "Result should be zero")
			assert.True(t, resp.IsError(), "IsError should be true")
		})
	}
}

func TestNewResponseError(t *testing.T) {
	t.Parallel()

	customErr := NewError(-32001, "Another Custom error")
	stdErr := errors.New("another standard error")
	internalErrWithData := ErrInternalError.WithData(stdErr.Error())

	//nolint:govet //Do not reorder struct
	testCases := []struct {
		name     string
		err      error
		expected *Response
	}{
		{
			name: "jsonrpc2 error",
			err:  customErr,
			expected: &Response{
				ID:    NewNullID(),
				Error: customErr,
			},
		},
		{
			name: "standard error",
			err:  stdErr,
			expected: &Response{
				ID:    NewNullID(),
				Error: internalErrWithData, // Expect it to be wrapped
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resp := NewResponseError(tc.err)

			assert.Equal(t, tc.expected.ID, resp.ID, "ID mismatch")
			assert.True(t, resp.ID.IsNull(), "ID should be null")
			assert.Equal(t, tc.expected.Error, resp.Error, "Error mismatch")
			assert.True(t, resp.Result.IsZero(), "Result should be zero")
			assert.True(t, resp.IsError(), "IsError should be true")
		})
	}
}

func TestResponse_IsError(t *testing.T) {
	t.Parallel()

	respResult := NewResponseWithResult(int64(1), "ok")
	respError := NewResponseWithError("id", errors.New("fail"))
	respNullIDError := NewResponseError(ErrInvalidRequest)

	assert.False(t, respResult.IsError())
	assert.True(t, respError.IsError())
	assert.True(t, respNullIDError.IsError())
}

func TestResponse_id(t *testing.T) {
	t.Parallel()

	idInt := NewID(int64(123))
	idStr := NewID("abc-456")
	idNull := NewNullID()

	respInt := &Response{ID: idInt, Result: NewResult("ok")}
	respStr := &Response{ID: idStr, Error: ErrInternalError}
	respNull := &Response{ID: idNull, Error: ErrParseError}

	assert.Equal(t, idInt, respInt.id())
	assert.Equal(t, idStr, respStr.id())
	assert.Equal(t, idNull, respNull.id())
}

func TestResponse_MarshalUnmarshalJSON(t *testing.T) {
	t.Parallel()

	customErr := NewError(-32000, "Custom server error")
	stdErr := errors.New("standard internal error")

	//nolint:govet //Do not reorder struct
	testCases := []struct {
		name         string
		response     *Response
		expectedJSON string
	}{
		{
			name:         "Response with result, null ID",
			response:     &Response{ID: NewNullID(), Result: NewResult("success data")},
			expectedJSON: `{"jsonrpc":"2.0","result":"success data","id":null}`,
		},
		{
			name:         "Response with result, int ID",
			response:     NewResponseWithResult(int64(1), "success data"),
			expectedJSON: `{"jsonrpc":"2.0","result":"success data","id":1}`,
		},
		{
			name:         "Response with result, string ID, object result",
			response:     NewResponseWithResult("req-abc", map[string]any{"status": "ok", "value": float64(123)}),
			expectedJSON: `{"jsonrpc":"2.0","result":{"status":"ok","value":123},"id":"req-abc"}`,
		},
		{
			name:         "Response with jsonrpc2 error, int ID",
			response:     NewResponseWithError(int64(2), customErr),
			expectedJSON: `{"jsonrpc":"2.0","error":{"code":-32000,"message":"Custom server error"},"id":2}`,
		},
		{
			name:         "Response with standard error, string ID",
			response:     NewResponseWithError("req-xyz", stdErr),
			expectedJSON: `{"jsonrpc":"2.0","error":{"code":-32603,"message":"Internal error","data":"standard internal error"},"id":"req-xyz"}`,
		},
		{
			name:         "Response with error, null ID",
			response:     NewResponseError(ErrInvalidRequest),
			expectedJSON: `{"jsonrpc":"2.0","error":{"code":-32600,"message":"Invalid Request"},"id":null}`,
		},
		{
			name:         "Response with result, null ID (spec violation, but test marshaling)",
			response:     &Response{ID: NewNullID(), Result: NewResult("data")},
			expectedJSON: `{"jsonrpc":"2.0","result":"data","id":null}`,
		},
	}

	for _, tc := range testCases {
		// Capture range variable
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Marshal
			jsonData, err := json.Marshal(tc.response)
			require.NoError(t, err, "Marshaling failed")
			assert.JSONEq(t, tc.expectedJSON, string(jsonData), "Marshaled JSON does not match expected")

			// Unmarshal
			var unmarshaledResp Response
			err = json.Unmarshal(jsonData, &unmarshaledResp)
			require.NoError(t, err, "Unmarshalling failed")

			// Prepare expected response for comparison after unmarshal adds Jsonrpc version
			expectedResp := *tc.response
			expectedResp.Jsonrpc = Version{present: true} // json.Marshal adds this

			// Compare basic fields
			assert.Equal(t, expectedResp.Jsonrpc, unmarshaledResp.Jsonrpc, "Jsonrpc version mismatch")
			assert.True(t, expectedResp.ID.Equal(unmarshaledResp.ID), "ID mismatch")

			// Compare Result
			if expectedResp.Result.IsZero() {
				assert.True(t, unmarshaledResp.Result.IsZero(), "Expected zero result, but got non-zero")
			} else {
				assert.False(t, unmarshaledResp.Result.IsZero(), "Expected non-zero result, but got zero")
				// Unmarshal results for deep comparison
				var unmarshaledResultVal any
				err = unmarshaledResp.Result.Unmarshal(&unmarshaledResultVal)
				require.NoError(t, err, "Failed to unmarshal actual result for comparison")
				assert.Equal(t, expectedResp.Result.Value(), unmarshaledResultVal, "Result content mismatch")
			}

			// Compare Error
			if expectedResp.Error.IsZero() {
				assert.True(t, unmarshaledResp.Error.IsZero(), "Expected zero error, but got non-zero")
			} else {
				assert.False(t, unmarshaledResp.Error.IsZero(), "Expected non-zero error, but got zero")
				// Compare error fields directly. For data, unmarshal if necessary.
				assert.Equal(t, expectedResp.Error.Code, unmarshaledResp.Error.Code, "Error code mismatch")
				assert.Equal(t, expectedResp.Error.Message, unmarshaledResp.Error.Message, "Error message mismatch")

				// Compare Error Data
				if expectedResp.Error.Data.IsZero() {
					assert.True(t, unmarshaledResp.Error.Data.IsZero(), "Expected zero error data, but got non-zero")
				} else {
					assert.False(t, unmarshaledResp.Error.Data.IsZero(), "Expected non-zero error data, but got zero")

					var unmarshaledErrDataVal any
					err = unmarshaledResp.Error.Data.Unmarshal(&unmarshaledErrDataVal)
					require.NoError(t, err, "Failed to unmarshal actual error data for comparison")
					assert.Equal(t, expectedResp.Error.Data.Value(), unmarshaledErrDataVal, "Error data content mismatch")
				}
			}
		})
	}
}
