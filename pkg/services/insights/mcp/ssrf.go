package mcp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Model-facing errors: they never echo the resolved address.
var (
	errKnowledgeURLBlocked  = errors.New("that page is not on the approved list for this room")
	errKnowledgeURLInsecure = errors.New("only https pages can be read")
	errKnowledgeFetchFailed = errors.New("the page could not be read right now")

	errExternalMCPURLBlocked  = errors.New("that mcp server address is not allowed")
	errExternalMCPURLInsecure = errors.New("mcp servers must use https")
	errExternalMCPPortBlocked = errors.New("mcp servers must use the default https port")
)

// blockedPrefixes are special-use ranges that must never be dialled, even when
// the address type checks alone would let them through (CGNAT metadata
// endpoints, documentation ranges, IPv4-mapped NAT64, Teredo and 6to4).
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

// blockedIP reports whether ip must never be dialled for external content.
// IPv4-mapped IPv6 addresses are unmapped first, so ::ffff:127.0.0.1 is
// rejected like 127.0.0.1.
func blockedIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	return blockedAddr(addr.Unmap())
}

func blockedAddr(addr netip.Addr) bool {
	if !addr.IsValid() {
		return true
	}
	if addr.IsUnspecified() ||
		!addr.IsGlobalUnicast() ||
		addr.IsPrivate() ||
		addr.IsLoopback() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsMulticast() ||
		addr.IsInterfaceLocalMulticast() {
		return true
	}
	for _, prefix := range blockedPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// urlGuard validates knowledge URLs before dialling.
//
// The scheme/host check runs before the request and again for every redirect,
// the Dialer.Control check runs on the address that DNS actually resolved, so a
// DNS rebind between validation and dial cannot reach a private address.
type urlGuard struct {
	allowHTTP bool
}

func (g urlGuard) validate(ctx context.Context, rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, errKnowledgeURLBlocked
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		if !g.allowHTTP {
			return nil, errKnowledgeURLInsecure
		}
	default:
		return nil, errKnowledgeURLBlocked
	}
	if parsed.User != nil {
		return nil, errKnowledgeURLBlocked
	}

	host := parsed.Hostname()
	if host == "" {
		return nil, errKnowledgeURLBlocked
	}
	if ip := net.ParseIP(host); ip != nil {
		if blockedIP(ip) {
			return nil, errKnowledgeURLBlocked
		}
		return parsed, nil
	}

	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
	if err != nil {
		return nil, errKnowledgeURLBlocked
	}
	for _, address := range addresses {
		if blockedIP(address.IP) {
			return nil, errKnowledgeURLBlocked
		}
	}
	return parsed, nil
}

// IsBlockedExternalIP reports whether ip must never be dialled for external
// content. It lets callers reject an obvious endpoint without a DNS lookup.
func IsBlockedExternalIP(ip net.IP) bool { return blockedIP(ip) }

// ValidateExternalMCPURL gates every external MCP endpoint before a session is
// opened. allowInsecureHTTP is a test-only escape hatch that also skips the IP
// and port checks, because loopback test servers cannot pass them.
func ValidateExternalMCPURL(ctx context.Context, rawURL string, allowInsecureHTTP bool) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, errExternalMCPURLBlocked
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		if !allowInsecureHTTP {
			return nil, errExternalMCPURLInsecure
		}
	default:
		return nil, errExternalMCPURLBlocked
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return nil, errExternalMCPURLBlocked
	}

	host := parsed.Hostname()
	if host == "" {
		return nil, errExternalMCPURLBlocked
	}
	if allowInsecureHTTP {
		return parsed, nil
	}

	// Internal services listen on their own ports; pinning https to 443 keeps
	// them out of reach even when the hostname resolves to a public address.
	if port := parsed.Port(); port != "" && port != "443" {
		return nil, errExternalMCPPortBlocked
	}

	if ip := net.ParseIP(host); ip != nil {
		if blockedIP(ip) {
			return nil, errExternalMCPURLBlocked
		}
		return parsed, nil
	}

	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
	if err != nil {
		return nil, errExternalMCPURLBlocked
	}
	for _, address := range addresses {
		if blockedIP(address.IP) {
			return nil, errExternalMCPURLBlocked
		}
	}
	return parsed, nil
}

// newGuardedHTTPClient returns a client that refuses private addresses at dial
// time and re-runs the guard for redirects.
func newGuardedHTTPClient(guard urlGuard) *http.Client {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return errKnowledgeURLBlocked
			}
			if blockedIP(net.ParseIP(host)) {
				return errKnowledgeURLBlocked
			}
			return nil
		},
	}
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 5 * time.Second,
			DisableKeepAlives:     true,
			MaxIdleConns:          1,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errKnowledgeURLBlocked
			}
			_, err := guard.validate(req.Context(), req.URL.String())
			return err
		},
	}
}
