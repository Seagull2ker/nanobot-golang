package security

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strings"
)

// blockedCIDRs covers all private, link-local, CGNAT, and test-net ranges.
// Combined with Go's built-in IsLoopback/IsPrivate/IsLinkLocal checks, this forms
// defense-in-depth SSRF protection that catches edge cases IsPrivate() misses.
var blockedCIDRs = []string{
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
	"127.0.0.0/8", "169.254.0.0/16", "::1/128",
	"fc00::/7", "fe80::/10", "100.64.0.0/10", "192.0.2.0/24",
}

var cidrNets []*net.IPNet

func init() {
	for _, cidr := range blockedCIDRs {
		_, n, err := net.ParseCIDR(cidr)
		if err == nil {
			cidrNets = append(cidrNets, n)
		}
	}
}

// IsBlockedIP returns true if the IP is private, loopback, link-local,
// unspecified, multicast, or within a known blocked CIDR range.
func IsBlockedIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	for _, n := range cidrNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ValidateURLTarget checks a URL for SSRF risks, including DNS rebinding protection.
// It blocks private IPs, metadata endpoints, localhost-like hostnames, and resolves
// the hostname to check all returned IPs against the blocked ranges.
func ValidateURLTarget(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("only http and https URLs are supported")
	}

	host := parsed.Hostname()

	// Block metadata endpoints and local-like hostnames.
	blockedHosts := []string{
		"metadata.google.internal", "169.254.169.254",
		"localhost", ".local", ".localhost",
	}
	for _, bh := range blockedHosts {
		if host == bh || strings.HasSuffix(host, bh) {
			return fmt.Errorf("URL targets internal/private host: %s", host)
		}
	}

	// DNS rebinding protection: resolve and check all IPs.
	ips, err := net.LookupIP(host)
	if err != nil {
		// If DNS fails, check if the host itself is an IP.
		if ip := net.ParseIP(host); ip != nil {
			if IsBlockedIP(ip) {
				return fmt.Errorf("URL targets blocked IP: %s", ip)
			}
		}
		return nil
	}

	for _, ip := range ips {
		if IsBlockedIP(ip) {
			return fmt.Errorf("URL %s resolves to blocked IP: %s", host, ip)
		}
	}

	return nil
}

// ValidatePathSafety checks that a resolved path stays within workspace boundaries.
// Returns an error if the path escapes the workspace via ".." traversal.
func ValidatePathSafety(path, workspace string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("abs path: %w", err)
	}
	absWS, err := filepath.Abs(workspace)
	if err != nil {
		return fmt.Errorf("abs workspace: %w", err)
	}

	rel, err := filepath.Rel(absWS, absPath)
	if err != nil {
		return fmt.Errorf("path outside workspace: %s", path)
	}
	if strings.HasPrefix(rel, "..") {
		return fmt.Errorf("path outside workspace: %s (resolved to %s)", path, absPath)
	}

	return nil
}

// SSRFError is returned for blocked URLs to distinguish security rejections
// from transient network errors. Tool safety middleware uses this to classify
// the error as non-retryable (hard policy boundary).
type SSRFError struct {
	URL    string
	Reason string
}

func (e *SSRFError) Error() string {
	return fmt.Sprintf("SSRF blocked: %s — %s", e.URL, e.Reason)
}
