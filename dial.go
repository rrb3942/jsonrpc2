package jsonrpc2

import (
	"context"
	"crypto/tls"
	"net"
	"net/url"
	"strings"
)

// Dial establishes a JSON-RPC 2.0 client connection to the specified destination URI.
// It returns a [*Client] ready for making RPC calls.
//
// The `destURI` format is `scheme:address` or an http/https URL.
//
// Supported Schemes:
//   - `tcp`, `tcp4`, `tcp6`: Establishes a standard TCP connection. Address is `host:port`.
//   - `udp`, `udp4`, `udp6`: Uses UDP. Address is `host:port`. Note: UDP is connectionless; `Dial` sets up the local side.
//   - `unix`, `unixgram`, `unixpacket`: Connects to a Unix domain socket. Address is the socket file path.
//   - `http`, `https`: Connects via HTTP(S). The full URL is used. Creates a client wrapping an [*HTTPBridge].
//   - `tls`: Establishes a TLS connection over TCP. Address is `host:port`. Uses default [*tls.Config].
//   - `tls4`: Establishes a TLS connection over TCPv4. Address is `host:port`. Uses default [*tls.Config].
//   - `tls6`: Establishes a TLS connection over TCPv6. Address is `host:port`. Uses default [*tls.Config].
//
// Examples:
//   - `tcp:127.0.0.1:9090`
//   - `tcp://localhost:9090`
//   - `udp::9090` (UDP to port 9090 on all interfaces)
//   - `http://127.0.0.1:8080/rpc`
//   - `https://api.example.com/jsonrpc`
//   - `unix:///tmp/mysocket.sock`
//   - `tls:127.0.0.1:9443`
//
// Returns [ErrUnknownScheme] if the scheme is not supported.
// Other errors may be returned from underlying network or TLS dialers, or URL parsing.
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

	return nil, ErrUnknownScheme // Return error for unsupported schemes
}

// dial is an internal helper to establish standard network connections (TCP, UDP, Unix).
// It uses net.Dialer to create the connection and wraps it using [NewClientIO].
func dial(ctx context.Context, network, addr string) (*Client, error) {
	// Use default net.Dialer with context support.
	conn, err := new(net.Dialer).DialContext(ctx, network, addr)

	if err != nil {
		return nil, err
	}

	// Wrap the connection with a new client using standard stream encoder/decoder.
	return NewClientIO(conn), nil
}

// dialHTTP is an internal helper for creating a client that communicates over HTTP/HTTPS.
// It initializes an [*HTTPBridge] which acts as both the [Encoder] and [Decoder]
// for the [*Client].
func dialHTTP(ctx context.Context, uri string) (*Client, error) {
	// Check context cancellation before proceeding.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	// Create the bridge that handles HTTP request/response logic.
	bridge := NewHTTPBridge(uri)

	// Create a client using the bridge for both encoding and decoding.
	return NewClient(bridge, bridge), nil
}

// dialTLS is an internal helper to establish TLS connections.
// It determines the underlying TCP network type (tcp, tcp4, tcp6) based on the
// input network string ("tls", "tls4", "tls6") and uses tls.Dialer.
// The resulting tls.Conn is wrapped using [NewClientIO].
func dialTLS(ctx context.Context, network, addr string) (*Client, error) {
	// Determine the underlying TCP network based on the "tls" prefix variant.
	tcpNetwork := "tcp" // Default for "tls"

	switch {
	case strings.HasSuffix(network, "6"): // "tls6"
		tcpNetwork = "tcp6"
	case strings.HasSuffix(network, "4"): // "tls4"
		tcpNetwork = "tcp4"
	}

	// Use default tls.Dialer with context support.
	// Note: This uses a nil tls.Config, resulting in default TLS settings.
	conn, err := new(tls.Dialer).DialContext(ctx, tcpNetwork, addr)

	if err != nil {
		return nil, err
	}

	// Wrap the TLS connection with a new client.
	return NewClientIO(conn), nil
}

// DialBasic is a convenience function that establishes a connection to the destination URI
// and returns a [*BasicClient] ready for making simplified RPC calls.
//
// It internally creates a [ClientPool] configured with MaxSize=1 and AcquireOnCreate=true,
// ensuring the connection is established and validated during the dial process.
// The underlying connection management and retries are handled by the pool.
//
// See [Dial] for details on supported URI schemes and behavior.
//
// Example:
//
//	bc, err := jsonrpc2.DialBasic(context.Background(), "tcp://localhost:9090")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer bc.Close()
//	bc.SetDefaultTimeout(5 * time.Second) // Optional: Set default timeout
//
//	var result string
//	err = bc.Call(context.Background(), "echo", jsonrpc2.NewParamsArray("hello"), &result)
//	// ... handle result/error ...
func DialBasic(ctx context.Context, destURI string) (*BasicClient, error) {
	// Configure a pool specifically for BasicClient:
	// - MaxSize: 1, as BasicClient represents a single logical connection.
	// - AcquireOnCreate: true, to validate the connection immediately.
	// - Use default timeouts/retries unless further configuration is added here.
	config := ClientPoolConfig{
		URI:             destURI,
		MaxSize:         1,
		AcquireOnCreate: true,
		// Inherit default DialTimeout, IdleTimeout, Retries from ClientPoolConfig defaults
	}

	// Create the pool using the default Dial function.
	pool, err := NewClientPool(ctx, config)
	if err != nil {
		return nil, err // Failed to create pool (likely connection failure)
	}

	// Wrap the pool in a BasicClient.
	// Initialize the ID randomly.
	//nolint:gosec // We just want to avoid always starting at 0
	return &BasicClient{pool: pool, id: rand.Uint32()}, nil
}
