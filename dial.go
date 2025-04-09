package jsonrpc2

import (
	"context"
	"net"
)

// Dial returns a new [*Client] to the given addr.
func Dial(ctx context.Context, network, addr string) (*Client, error) {
	conn, err := new(net.Dialer).DialContext(ctx, network, addr)

	if err != nil {
		return nil, err
	}

	return NewClientIO(conn), nil
}

// DialHTTP returns a new [*Client] that uses an [*HTTPBridge] to url for communication.
func DialHTTP(ctx context.Context, url string) (*Client, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	bridge := NewHTTPBridge(url)

	return NewClient(bridge, bridge), nil
}

// DialBasic returns a new [*BasicClient] to the given addr.
func DialBasic(ctx context.Context, network, addr string) (*BasicClient, error) {
	conn, err := new(net.Dialer).DialContext(ctx, network, addr)

	if err != nil {
		return nil, err
	}

	return NewBasicClientIO(conn), nil
}

// DialBasicHTTP returns a new [*BasicClient] that uses an [*HTTPBridge] to url for communication.
func DialBasicHTTP(ctx context.Context, url string) (*BasicClient, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	bridge := NewHTTPBridge(url)

	return NewBasicClient(bridge, bridge), nil
}
