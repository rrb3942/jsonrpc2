package jsonrpc2

import (
	"encoding/json"
)

// RawRequest represents a fully encoded [*Request].
// It may be used in certain client calls for sending specially formed requests.
// When using a RawRequest, no encoding checking is done to ensure its validity.
type RawRequest json.RawMessage

// RawNotification represents a fully encoded [*Request] that is a notification.
// It may be used in certain client calls for sending specially formed notifications.
// When using a RawNotification, no encoding checking is done to ensure its validity.
type RawNotification json.RawMessage

// RawRequest represents a fully encoded [*Response]
// It may be used as a result from the [Handler] to provided a specially formed response.
// When using a RawResponse, no encoding checking is done to ensure its validity.
type RawResponse json.RawMessage
