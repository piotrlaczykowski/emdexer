package util

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// IsPrivateIP checks if an IP belongs to private or reserved ranges.
func IsPrivateIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}

	// RFC 1918 & RFC 4193 (IPv6 Unique Local)
	privateIPBlocks := []*net.IPNet{
		{IP: net.ParseIP("10.0.0.0"), Mask: net.CIDRMask(8, 32)},
		{IP: net.ParseIP("172.16.0.0"), Mask: net.CIDRMask(12, 32)},
		{IP: net.ParseIP("192.168.0.0"), Mask: net.CIDRMask(16, 32)},
		{IP: net.ParseIP("100.64.0.0"), Mask: net.CIDRMask(10, 32)}, // Carrier-grade NAT
		{IP: net.ParseIP("fc00::"), Mask: net.CIDRMask(7, 128)},     // IPv6 ULA
	}

	for _, block := range privateIPBlocks {
		if block.Contains(ip) {
			return true
		}
	}

	return false
}

// NewSafeTransport returns an http.Transport with an SSRF guard.
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
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ForceAttemptHTTP2:     true,
	}
}

// NewSafeHTTPClient returns an http.Client using the SafeTransport.
func NewSafeHTTPClient(timeout time.Duration) *http.Client {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &http.Client{
		Transport: NewSafeTransport(),
		Timeout:   timeout,
	}
}
