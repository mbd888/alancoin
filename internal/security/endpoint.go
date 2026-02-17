package security

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ValidateEndpointURL checks that an endpoint URL is safe for server-side requests.
// Blocks private, loopback, link-local, and unspecified IPs to prevent SSRF attacks.
// Both the literal host and DNS-resolved addresses are checked.
func ValidateEndpointURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL format")
	}

	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("URL scheme must be http or https")
	}

	if u.Host == "" {
		return fmt.Errorf("URL must have a host")
	}

	host := u.Hostname()

	// Block known internal hostnames
	blocked := []string{"localhost", "metadata.google.internal", "metadata.google"}
	for _, b := range blocked {
		if strings.EqualFold(host, b) {
			return fmt.Errorf("URL host %q is not allowed", host)
		}
	}

	// Block private/loopback/link-local IP literals
	ip := net.ParseIP(host)
	if ip != nil {
		if err := checkIP(ip); err != nil {
			return err
		}
		return nil // IP literal checked, no DNS resolution needed
	}

	// Resolve hostname and check all resolved IPs
	ips, err := net.LookupHost(host)
	if err != nil {
		return fmt.Errorf("cannot resolve URL host: %s", host)
	}
	for _, ipStr := range ips {
		resolved := net.ParseIP(ipStr)
		if resolved != nil {
			if err := checkIP(resolved); err != nil {
				return fmt.Errorf("URL host %q resolves to blocked address: %v", host, err)
			}
		}
	}

	return nil
}

func checkIP(ip net.IP) error {
	if ip.IsLoopback() {
		return fmt.Errorf("loopback addresses are not allowed")
	}
	if ip.IsPrivate() {
		return fmt.Errorf("private addresses are not allowed")
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return fmt.Errorf("link-local addresses are not allowed")
	}
	if ip.IsUnspecified() {
		return fmt.Errorf("unspecified addresses are not allowed")
	}
	return nil
}
