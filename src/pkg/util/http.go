package util

import (
	"context"
	"fmt"
	"net"
	"net/http"
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

// NewSafeTransport returns an http.Transport with an SSRF guard that supports hostnames.
func NewSafeTransport() *http.Transport {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		// We use a custom resolver logic inside DialContext rather than the Control hook
		// because the hook doesn't easily allow for multi-IP validation before connection.
	}

	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}

			// Resolve hostnames to all IPs
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				// If resolution fails, it might be a literal IP address.
				// Try parsing it directly.
				if ip := net.ParseIP(host); ip != nil {
					if IsPrivateIP(ip) {
						return nil, fmt.Errorf("ssrf-guard: dial to restricted IP %s blocked", ip)
					}
					return dialer.DialContext(ctx, network, addr)
				}
				return nil, fmt.Errorf("ssrf-guard: resolution failed for %s: %w", host, err)
			}

			if len(ips) == 0 {
				return nil, fmt.Errorf("ssrf-guard: no IP addresses found for %s", host)
			}

			// Validate EVERY resolved IP
			for _, ip := range ips {
				if IsPrivateIP(ip.IP) {
					return nil, fmt.Errorf("ssrf-guard: dial to restricted IP %s blocked for host %s", ip.IP, host)
				}
			}

			// If all IPs are safe, use the standard dialer but with the FIRST resolved IP
			// to prevent DNS rebinding attacks.
			_, port, _ := net.SplitHostPort(addr)
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ForceAttemptHTTP2:     true,
	}
}

// NewSafeHTTPClient returns an http.Client using the SafeTransport.
func NewSafeHTTPClient(timeout time.Duration) *http.Client {
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	return &http.Client{
		Transport: NewSafeTransport(),
		Timeout:   timeout,
	}
}
