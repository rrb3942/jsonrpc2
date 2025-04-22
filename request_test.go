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
	assert := assert.New(t)

	// Test with int64 ID
	reqInt := NewRequest(int64(1), "testMethodInt")
	assert.Equal("testMethodInt", reqInt.Method)
	assert.False(reqInt.ID.IsZero())
	assert.False(reqInt.ID.IsNull())
	idInt, err := reqInt.ID.Int64()
	assert.NoError(err)
	assert.Equal(int64(1), idInt)
	assert.True(reqInt.Params.IsZero())

	// Test with string ID
	reqStr := NewRequest("req-id-string", "testMethodStr")
	assert.Equal("testMethodStr", reqStr.Method)
	assert.False(reqStr.ID.IsZero())
	assert.False(reqStr.ID.IsNull())
	idStr, ok := reqStr.ID.String()
	assert.True(ok)
	assert.Equal("req-id-string", idStr)
	assert.True(reqStr.Params.IsZero())
}

func TestNewRequestWithParams(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)

	params := NewParamsObject(map[string]any{"key": "value"})
	req := NewRequestWithParams(int64(2), "methodWithParams", params)

	assert.Equal("methodWithParams", req.Method)
	assert.False(req.ID.IsZero())
	idInt, err := req.ID.Int64()
	assert.NoError(err)
	assert.Equal(int64(2), idInt)

	assert.False(req.Params.IsZero())
	var decodedParams map[string]any
	err = req.Params.Unmarshal(&decodedParams)
	require.NoError(err)
	assert.Equal(map[string]any{"key": "value"}, decodedParams)
}

func TestRequest_ResponseWithError(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	req := NewRequest(int64(3), "errorMethod")

	// Test with standard error
	stdErr := errors.New("standard error occurred")
	respStdErr := req.ResponseWithError(stdErr)
	assert.Equal(req.ID, respStdErr.ID)
	assert.True(respStdErr.Result.IsZero())
	assert.False(respStdErr.Error.IsZero())
	assert.Equal(ErrInternalError.err.Code, respStdErr.Error.err.Code)
	assert.Equal(ErrInternalError.err.Message, respStdErr.Error.err.Message)
	var dataStr string
	err := respStdErr.Error.Data().Unmarshal(&dataStr)
	assert.NoError(err)
	assert.Equal(stdErr.Error(), dataStr)

	// Test with jsonrpc2.Error
	rpcErr := NewError(-32000, "RPC specific error")
	respRPCErr := req.ResponseWithError(rpcErr)
	assert.Equal(req.ID, respRPCErr.ID)
	assert.True(respRPCErr.Result.IsZero())
	assert.False(respRPCErr.Error.IsZero())
	assert.Equal(rpcErr.err.Code, respRPCErr.Error.err.Code)
	assert.Equal(rpcErr.err.Message, respRPCErr.Error.err.Message)
	assert.True(respRPCErr.Error.Data().IsZero()) // No data added in this case
}

func TestRequest_ResponseWithResult(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)

	req := NewRequest("result-req", "resultMethod")
	resultData := map[string]any{"status": "success", "value": 123}
	resp := req.ResponseWithResult(resultData)

	assert.Equal(req.ID, resp.ID)
	assert.True(resp.Error.IsZero())
	assert.False(resp.Result.IsZero())

	var decodedResult map[string]any
	err := resp.Result.Unmarshal(&decodedResult)
	require.NoError(err)
	// JSON numbers are decoded as float64 by default
	assert.Equal("success", decodedResult["status"])
	assert.Equal(float64(123), decodedResult["value"])
}

func TestRequest_IsNotification(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	// Regular request
	req := NewRequest(int64(1), "method")
	assert.False(req.IsNotification())

	// Notification (created via Request struct directly with zero ID)
	notification := &Request{Method: "notifyMethod"} // ID is zero value
	assert.True(notification.IsNotification())

	// Notification (created via NewNotification)
	notificationProper := NewNotification("notifyMethodProper")
	assert.True(notificationProper.AsRequest().IsNotification())
}

func TestRequest_AsNotification(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	// Regular request
	req := NewRequest(int64(1), "method")
	assert.Nil(req.AsNotification())

	// Notification (created via Request struct directly with zero ID)
	notificationReq := &Request{Method: "notifyMethod"} // ID is zero value
	notif := notificationReq.AsNotification()
	assert.NotNil(notif)
	assert.Equal("notifyMethod", notif.Method)
	assert.True(notif.ID.IsZero()) // Should still be zero

	// Test nil receiver
	var nilReq *Request
	assert.Nil(nilReq.AsNotification())
}

func TestRequest_id(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	// Request with ID
	reqWithID := NewRequest(int64(123), "method")
	assert.Equal(NewID(int64(123)), reqWithID.id())

	// Notification (no ID)
	notification := NewNotification("notify")
	assert.True(notification.AsRequest().id().IsZero())
}

// Helper to create Params from an object easily for tests
func NewParamsObject(v any) Params {
	raw, _ := json.Marshal(v)
	p := Params{}
	_ = p.UnmarshalJSON(raw)
	return p
}
