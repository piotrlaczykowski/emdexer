// Package safenet provides SSRF-safe HTTP clients that block connections
// to private and reserved IP ranges at dial time, preventing DNS rebinding attacks.
package safenet

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// privateIPBlocks contains RFC 1918 private address ranges.
var privateIPBlocks = []*net.IPNet{
	{IP: net.ParseIP("10.0.0.0"), Mask: net.CIDRMask(8, 32)},
	{IP: net.ParseIP("172.16.0.0"), Mask: net.CIDRMask(12, 32)},
	{IP: net.ParseIP("192.168.0.0"), Mask: net.CIDRMask(16, 32)},
}

// IsPrivateIP checks if an IP belongs to private or reserved ranges
// (loopback, link-local, RFC 1918).
func IsPrivateIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}

	for _, block := range privateIPBlocks {
		if block.Contains(ip) {
			return true
		}
	}

	return false
}

// NewSafeTransport returns an *http.Transport that validates destination IPs
// at dial time, blocking connections to private/reserved ranges.
func NewSafeTransport() *http.Transport {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("ssrf-guard: could not parse dial address %q: %w", address, err)
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("ssrf-guard: non-IP address at dial time: %q", host)
			}
			if IsPrivateIP(ip) {
				return fmt.Errorf("ssrf-guard: dial to restricted IP %s blocked (DNS rebinding?)", ip)
			}
			return nil
		},
	}
	return &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:  10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ForceAttemptHTTP2:     true,
	}
}

// NewSafeClient returns an *http.Client with SSRF-safe transport and the given timeout.
func NewSafeClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: NewSafeTransport(),
		Timeout:   timeout,
	}
}
