package jsonrpc2

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// findFreePort asks the kernel for a free open port that is ready to use.
func findFreePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0") // Use 127.0.0.1 to ensure it's local
	require.NoError(t, err)
	defer l.Close()
	addr := l.Addr().String()
	// For TCP, just closing the listener might not free the port immediately (TIME_WAIT).
	// Let's try listening on UDP as well, which is usually less problematic.
	ludp, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ludp.Close()
	addrUDP := ludp.LocalAddr().String()

	// Prefer the TCP port address format if possible
	_, port, err := net.SplitHostPort(addr)
	if err == nil {
		return "127.0.0.1:" + port
	}
	// Fallback to UDP address format
	_, port, err = net.SplitHostPort(addrUDP)
	require.NoError(t, err) // Should not fail if UDP listen succeeded
	return "127.0.0.1:" + port
}

// findUnusedPort finds a port that is likely unused without binding to it.
// Note: This is less reliable than findFreePort due to potential race conditions,
// but useful for testing connection refused errors.
func findUnusedPort(t *testing.T) string {
	t.Helper()
	// Try a high port number first
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err == nil {
		defer l.Close()
		addr := l.Addr().String()
		_, port, _ := net.SplitHostPort(addr)
		// Immediately close and hope it's unused for a short while
		return "127.0.0.1:" + port
	}
	// Fallback to a common range if the dynamic port failed (less likely)
	return "127.0.0.1:65534" // A common high port unlikely to be in use
}

func TestDial(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// --- TCP Tests ---
	t.Run("TCP_Success", func(t *testing.T) {
		addr := findFreePort(t)
		ln, err := net.Listen("tcp", addr)
		require.NoError(t, err)
		defer ln.Close()

		go func() {
			conn, _ := ln.Accept()
			if conn != nil {
				conn.Close()
			}
		}()

		client, err := Dial(ctx, "tcp:"+addr)
		require.NoError(t, err)
		require.NotNil(t, client)
		assert.NoError(t, client.Close())
	})

	t.Run("TCP_Failure_Refused", func(t *testing.T) {
		addr := findUnusedPort(t)
		_, err := Dial(ctx, "tcp:"+addr)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "connection refused", "Expected connection refused error")
	})

	// --- UDP Tests ---
	// Note: UDP Dial doesn't establish a connection, so "success" means no immediate error.
	t.Run("UDP_Success", func(t *testing.T) {
		// Dialing UDP to a random port usually succeeds immediately.
		addr := findFreePort(t) // Get a valid address format
		client, err := Dial(ctx, "udp:"+addr)
		require.NoError(t, err)
		require.NotNil(t, client)
		// UDP doesn't have a persistent connection in the same way TCP does,
		// but the client still needs closing.
		assert.NoError(t, client.Close())
	})

	t.Run("UDP_Failure_InvalidAddr", func(t *testing.T) {
		_, err := Dial(ctx, "udp:invalid-address:!!")
		require.Error(t, err)
		// Error message varies by OS, check for common patterns
		assert.Contains(t, strings.ToLower(err.Error()), "address", "Expected address-related error")
	})

	// --- Unix Socket Tests ---
	t.Run("Unix_Success", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Unix sockets not supported on Windows")
		}
		tmpDir := t.TempDir()
		sockPath := filepath.Join(tmpDir, "test.sock")
		ln, err := net.Listen("unix", sockPath)
		require.NoError(t, err)
		defer ln.Close() // Also removes the socket file on most systems

		go func() {
			conn, _ := ln.Accept()
			if conn != nil {
				conn.Close()
			}
		}()

		client, err := Dial(ctx, "unix://"+sockPath)
		require.NoError(t, err)
		require.NotNil(t, client)
		assert.NoError(t, client.Close())
	})

	t.Run("Unix_Failure_NotFound", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Unix sockets not supported on Windows")
		}
		tmpDir := t.TempDir()
		sockPath := filepath.Join(tmpDir, "nonexistent.sock")
		_, err := Dial(ctx, "unix://"+sockPath)
		require.Error(t, err)
		// Check if the error is related to "no such file or directory"
		assert.ErrorContains(t, err, "no such file or directory")
	})

	// --- HTTP Tests ---
	t.Run("HTTP_Success", func(t *testing.T) {
		// We don't need a real server, just need Dial to create the HTTPBridge client
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// No need to actually handle RPC, just need the server running
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client, err := Dial(ctx, server.URL) // Use the test server's URL
		require.NoError(t, err)
		require.NotNil(t, client)
		// Check if the underlying encoder/decoder is an HTTPBridge
		_, okE := client.e.(*HTTPBridge)
		_, okD := client.d.(*HTTPBridge)
		assert.True(t, okE, "Encoder should be HTTPBridge")
		assert.True(t, okD, "Decoder should be HTTPBridge")
		assert.NoError(t, client.Close()) // HTTPBridge close is a no-op currently
	})

	t.Run("HTTP_Failure_InvalidURL", func(t *testing.T) {
		_, err := Dial(ctx, "http://invalid host") // Space in host is invalid
		require.Error(t, err)
		var urlErr *url.Error
		assert.ErrorAs(t, err, &urlErr, "Expected a URL parsing error")
	})

	// --- TLS Tests (Connection Refused Only) ---
	t.Run("TLS_Failure_Refused", func(t *testing.T) {
		addr := findUnusedPort(t)
		_, err := Dial(ctx, "tls:"+addr)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "connection refused", "Expected connection refused error for tls")
	})

	t.Run("TLS4_Failure_Refused", func(t *testing.T) {
		addr := findUnusedPort(t)
		_, err := Dial(ctx, "tls4:"+addr)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "connection refused", "Expected connection refused error for tls4")
	})

	t.Run("TLS6_Failure_Refused", func(t *testing.T) {
		addr := findUnusedPort(t)
		// Ensure IPv6 address format if needed, though localhost often resolves for both
		host, port, _ := net.SplitHostPort(addr)
		if host == "127.0.0.1" {
			host = "[::1]" // Use IPv6 loopback
		}
		addrV6 := net.JoinHostPort(host, port)

		_, err := Dial(ctx, "tls6:"+addrV6)
		// Note: This might fail differently if IPv6 isn't properly configured on the host.
		// The primary goal is to test the Dial logic routes to dialTLS and fails.
		// Connection refused is the most likely error if the port is unused.
		require.Error(t, err)
		assert.Contains(t, err.Error(), "connection refused", "Expected connection refused error for tls6")
	})

	// --- Invalid Scheme ---
	t.Run("InvalidScheme", func(t *testing.T) {
		_, err := Dial(ctx, "invalid:scheme")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrUnknownScheme)
	})

	// --- Invalid URI Format ---
	t.Run("InvalidURI", func(t *testing.T) {
		_, err := Dial(ctx, "::") // Invalid URI format
		require.Error(t, err)
		var urlErr *url.Error
		assert.ErrorAs(t, err, &urlErr, "Expected a URL parsing error")
	})

	// --- Context Cancelled ---
	t.Run("ContextCancelled", func(t *testing.T) {
		cctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		addr := findFreePort(t) // Need a valid target address format
		_, err := Dial(cctx, "tcp:"+addr)
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	})

	// --- Context Deadline Exceeded ---
	t.Run("ContextDeadlineExceeded", func(t *testing.T) {
		dctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond) // Tiny deadline
		defer cancel()
		time.Sleep(1 * time.Millisecond) // Ensure deadline passes

		addr := "tcp:192.0.2.1:12345" // Use a blackhole address to ensure dial hangs until deadline
		_, err := Dial(dctx, addr)
		require.Error(t, err)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})
}
