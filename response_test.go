package jsonrpc2

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
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
	respNull := &Response{ID: idNull, Error: ErrParse}

	assert.Equal(t, idInt, respInt.id())
	assert.Equal(t, idStr, respStr.id())
	assert.Equal(t, idNull, respNull.id())
}
