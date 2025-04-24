package jsonrpc2

import (
	"context"
	"crypto/tls"
	"net"
	"net/url"
	"strings"
)

// Dial creates a client connection to the given uri.
//
// Supported schemes: tcp, tcp4, tcp6, udp, udp4, udp6, unix, unixgram, unixpacket, http, tls (tls over tcp), tls6 (tls over tcp6)
//
// TLS connections use the default go [tls.Config].
//
// If destURI is an http url, a client wrapping [HTTPBridge] will automatically be created.
//
// Examples uris: 'tcp:127.0.0.1:9090', 'udp::9090', 'http://127.0.0.1:8080/rpc', 'unix:///tmp/mysocket'
func Dial(ctx context.Context, destURI string) (*Client, error) {
	uri, err := url.Parse(destURI)

	if err != nil {
		return nil, err
	}

	switch {
	case strings.HasPrefix(uri.Scheme, "tcp"), strings.HasPrefix(uri.Scheme, "udp"):
		return dial(ctx, uri.Scheme, strings.TrimPrefix(destURI, uri.Scheme+":"))
	case strings.HasPrefix(uri.Scheme, "tls"):
		return dialTLS(ctx, uri.Scheme, strings.TrimPrefix(destURI, uri.Scheme+":"))
	case strings.HasPrefix(uri.Scheme, "unix"):
		return dial(ctx, uri.Scheme, uri.Path)
	case strings.HasPrefix(uri.Scheme, "http"):
		return dialHTTP(ctx, destURI)
	}

	return nil, ErrUnknownScheme
}

// Dial returns a new [*Client] to the given addr.
func dial(ctx context.Context, network, addr string) (*Client, error) {
	conn, err := new(net.Dialer).DialContext(ctx, network, addr)

	if err != nil {
		return nil, err
	}

	return NewClientIO(conn), nil
}

// DialHTTP returns a new [*Client] that uses an [*HTTPBridge] to url for communication.
func dialHTTP(ctx context.Context, uri string) (*Client, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	bridge := NewHTTPBridge(uri)

	return NewClient(bridge, bridge), nil
}

// DialTLS returns a new [*Client] over a tls connection.
func dialTLS(ctx context.Context, network, addr string) (*Client, error) {
	switch {
	case strings.HasSuffix(network, "6"):
		network = "tcp6"
	default:
		network = "tcp"
	}

	conn, err := new(tls.Dialer).DialContext(ctx, network, addr)

	if err != nil {
		return nil, err
	}

	return NewClientIO(conn), nil
}

// DialBasic returns a new [*BasicClient] to the given uri.
//
// See [Dial] for supported uri formats.
func DialBasic(ctx context.Context, destURI string) (*BasicClient, error) {
	client, err := Dial(ctx, destURI)

	if err != nil {
		return nil, err
	}

	return &BasicClient{client: client}, nil
}
