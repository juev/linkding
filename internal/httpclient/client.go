// Package httpclient guards outbound requests to URLs supplied by users.
package httpclient

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

type allowlist struct {
	allowAll bool
	hosts    []string
	networks []netip.Prefix
}

func parseAllowlist(raw string) allowlist {
	var result allowlist
	for _, item := range strings.Split(strings.ToLower(raw), ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if item == "*" {
			result.allowAll = true
			continue
		}
		item = strings.TrimPrefix(strings.TrimSuffix(item, "]"), "[")
		if prefix, err := netip.ParsePrefix(item); err == nil {
			result.networks = append(result.networks, prefix.Masked())
			continue
		}
		if addr, err := netip.ParseAddr(item); err == nil {
			result.networks = append(result.networks, netip.PrefixFrom(addr.Unmap(), addr.Unmap().BitLen()))
			continue
		}
		if !strings.Contains(item, "/") {
			result.hosts = append(result.hosts, item)
		}
	}
	return result
}

func (a allowlist) matchesHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	for _, allowed := range a.hosts {
		if strings.HasPrefix(allowed, ".") {
			if host == allowed[1:] || strings.HasSuffix(host, allowed) {
				return true
			}
		} else if host == allowed {
			return true
		}
	}
	return false
}

func (a allowlist) matchesAddress(value string) bool {
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	for _, prefix := range a.networks {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

var reserved = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("::/96"), netip.MustParsePrefix("fec0::/10"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
}

func publicAddress(value string) bool {
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsUnspecified() {
		return false
	}
	for _, prefix := range reserved {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

type BlockedAddressError struct {
	Host    string
	Address string
}

func (e *BlockedAddressError) Error() string {
	return fmt.Sprintf("refusing to connect to %s, which resolves to non-public address %s", e.Host, e.Address)
}

// New checks every resolved address at connection time, then dials an already
// checked IP. The original hostname remains available for HTTP Host and TLS.
func New(allowedInternalHosts string, timeout time.Duration) *http.Client {
	allowed := parseAllowlist(allowedInternalHosts)
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	transport := &http.Transport{Proxy: nil}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if allowed.allowAll || allowed.matchesHost(host) {
			return dialer.DialContext(ctx, network, address)
		}
		resolved, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(resolved) == 0 {
			return nil, fmt.Errorf("no address for %s", host)
		}
		for _, item := range resolved {
			ip := item.IP.String()
			if !allowed.matchesAddress(ip) && !publicAddress(ip) {
				return nil, &BlockedAddressError{Host: host, Address: ip}
			}
		}
		var lastErr error
		for _, item := range resolved {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(item.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
	return &http.Client{Transport: transport, Timeout: timeout}
}
