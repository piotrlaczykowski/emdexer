package safenet

import (
	"net"
	"testing"
)

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		ip      string
		private bool
	}{
		// Loopback
		{"127.0.0.1", true},
		{"127.0.0.2", true},

		// RFC 1918
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.0.1", true},
		{"192.168.255.255", true},

		// Link-local
		{"169.254.1.1", true},

		// Public IPs
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"203.0.113.1", false},

		// Edge cases — just outside private ranges
		{"172.32.0.1", false},
		{"11.0.0.1", false},
	}

	for _, tt := range tests {
		ip := net.ParseIP(tt.ip)
		if ip == nil {
			t.Fatalf("failed to parse test IP %q", tt.ip)
		}
		got := IsPrivateIP(ip)
		if got != tt.private {
			t.Errorf("IsPrivateIP(%s) = %v, want %v", tt.ip, got, tt.private)
		}
	}
}

func TestNewSafeTransport_BlocksPrivateIP(t *testing.T) {
	transport := NewSafeTransport()
	if transport == nil {
		t.Fatal("NewSafeTransport returned nil")
	}
	if transport.DialContext == nil {
		t.Fatal("NewSafeTransport has nil DialContext")
	}
}

func TestNewSafeClient(t *testing.T) {
	client := NewSafeClient(30_000_000_000) // 30s
	if client == nil {
		t.Fatal("NewSafeClient returned nil")
	}
	if client.Transport == nil {
		t.Fatal("NewSafeClient has nil Transport")
	}
}
