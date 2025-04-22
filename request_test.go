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
	tassert := assert.New(t)

	// Test with int64 ID
	reqInt := NewRequest(int64(1), "testMethodInt")
	tassert.Equal("testMethodInt", reqInt.Method)
	tassert.False(reqInt.ID.IsZero())
	tassert.False(reqInt.ID.IsNull())
	idInt, err := reqInt.ID.Int64()
	tassert.NoError(err)
	tassert.Equal(int64(1), idInt)
	tassert.True(reqInt.Params.IsZero())

	// Test with string ID
	reqStr := NewRequest("req-id-string", "testMethodStr")
	tassert.Equal("testMethodStr", reqStr.Method)
	tassert.False(reqStr.ID.IsZero())
	tassert.False(reqStr.ID.IsNull())
	idStr, ok := reqStr.ID.String()
	tassert.True(ok)
	tassert.Equal("req-id-string", idStr)
	tassert.True(reqStr.Params.IsZero())
}

func TestNewRequestWithParams(t *testing.T) {
	t.Parallel()
	tassert := assert.New(t)
	trequire := require.New(t)

	params := NewParamsJSONObject(map[string]any{"key": "value"})
	req := NewRequestWithParams(int64(2), "methodWithParams", params)

	tassert.Equal("methodWithParams", req.Method)
	tassert.False(req.ID.IsZero())
	idInt, err := req.ID.Int64()
	tassert.NoError(err)
	tassert.Equal(int64(2), idInt)

	tassert.False(req.Params.IsZero())

	var decodedParams map[string]any
	err = req.Params.Unmarshal(&decodedParams)
	trequire.NoError(err)
	tassert.Equal(map[string]any{"key": "value"}, decodedParams)
}

func TestRequest_ResponseWithError(t *testing.T) {
	t.Parallel()
	tassert := assert.New(t)

	req := NewRequest(int64(3), "errorMethod")

	// Test with standard error
	stdErr := errors.New("standard error occurred")
	respStdErr := req.ResponseWithError(stdErr)
	tassert.Equal(req.ID, respStdErr.ID)
	tassert.True(respStdErr.Result.IsZero())
	tassert.False(respStdErr.Error.IsZero())
	tassert.Equal(ErrInternalError.err.Code, respStdErr.Error.err.Code)
	tassert.Equal(ErrInternalError.err.Message, respStdErr.Error.err.Message)

	gotData := respStdErr.Error.Data().Value()
	tassert.Equal(stdErr.Error(), gotData)

	// Test with jsonrpc2.Error
	rpcErr := NewError(-32000, "RPC specific error")
	respRPCErr := req.ResponseWithError(rpcErr)
	tassert.Equal(req.ID, respRPCErr.ID)
	tassert.True(respRPCErr.Result.IsZero())
	tassert.False(respRPCErr.Error.IsZero())
	tassert.Equal(rpcErr.err.Code, respRPCErr.Error.err.Code)
	tassert.Equal(rpcErr.err.Message, respRPCErr.Error.err.Message)
	tassert.True(respRPCErr.Error.Data().IsZero()) // No data added in this case
}

func TestRequest_ResponseWithResult(t *testing.T) {
	t.Parallel()
	tassert := assert.New(t)

	req := NewRequest("result-req", "resultMethod")
	resultData := map[string]any{"status": "success", "value": 123}
	resp := req.ResponseWithResult(resultData)

	tassert.Equal(req.ID, resp.ID)
	tassert.True(resp.Error.IsZero())
	tassert.False(resp.Result.IsZero())

	gotResult := resp.Result.Value()

	tassert.Equal(gotResult, resultData)
}

func TestRequest_IsNotification(t *testing.T) {
	t.Parallel()
	tassert := assert.New(t)

	// Regular request
	req := NewRequest(int64(1), "method")
	tassert.False(req.IsNotification())

	// Notification (created via Request struct directly with zero ID)
	notification := &Request{Method: "notifyMethod"} // ID is zero value
	tassert.True(notification.IsNotification())

	// Notification (created via NewNotification)
	notificationProper := NewNotification("notifyMethodProper")
	tassert.True(notificationProper.AsRequest().IsNotification())
}

func TestRequest_AsNotification(t *testing.T) {
	t.Parallel()
	tassert := assert.New(t)

	// Regular request
	req := NewRequest(int64(1), "method")
	tassert.Nil(req.AsNotification())

	// Notification (created via Request struct directly with zero ID)
	notificationReq := &Request{Method: "notifyMethod"} // ID is zero value
	notif := notificationReq.AsNotification()
	tassert.NotNil(notif)
	tassert.Equal("notifyMethod", notif.Method)
	tassert.True(notif.ID.IsZero()) // Should still be zero

	// Test nil receiver
	var nilReq *Request

	tassert.Nil(nilReq.AsNotification())
}

func TestRequest_id(t *testing.T) {
	t.Parallel()
	tassert := assert.New(t)

	// Request with ID
	reqWithID := NewRequest(int64(123), "method")
	tassert.Equal(NewID(int64(123)), reqWithID.id())

	// Notification (no ID)
	notification := NewNotification("notify")
	id := notification.AsRequest().id()
	tassert.True(id.IsZero())
}

// Helper to create Params from an object easily for tests.
func NewParamsJSONObject(v any) Params {
	//nolint:errchkjson //Helper func for testing
	raw, _ := json.Marshal(v)
	p := Params{}
	_ = p.UnmarshalJSON(raw)

	return p
}
