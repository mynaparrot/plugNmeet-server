package mcp

// Verification of everything the server fetches from, or renders to the model
// on behalf of, an untrusted source: host and IP classification, URL and
// endpoint gates, the guarded HTTP client, and the sanitisers applied to
// external text before it reaches the model.

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/mynaparrot/plugnmeet-protocol/hooks"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
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

// ErrInvalidExternalURL marks a host-supplied external URL that fails the built-in
// checks, so a caller can map it to the feature-specific error of its own API.
var ErrInvalidExternalURL = errors.New("external url is not allowed")

// ---------------------------------------------------------------------------
// IP & hostname verification
// ---------------------------------------------------------------------------

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

// IsBlockedExternalIP reports whether ip must never be dialled for external
// content. It lets callers reject an obvious endpoint without a DNS lookup.
func IsBlockedExternalIP(ip net.IP) bool { return blockedIP(ip) }

// IsInternalHost rejects the hostnames that can never name a public service,
// without a DNS lookup: localhost, single-label names that only resolve through
// an internal search domain, and the reserved internal suffixes.
func IsInternalHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "localhost" || !strings.Contains(host, ".") {
		return true
	}
	for _, suffix := range []string{".localhost", ".local", ".internal"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

// externalResolveTimeout bounds every verification lookup, so creating a room
// or reading a page never waits on a slow resolver.
const externalResolveTimeout = 2 * time.Second

// lookupIPAddr resolves a host for the verification helpers. Tests replace it to
// keep the checks deterministic.
var lookupIPAddr = net.DefaultResolver.LookupIPAddr

// validateResolvedHost reports whether host may be dialled: the name must not be
// internal and every address it resolves to must be public. IP literals skip the
// lookup. failOnLookupErr decides what a failed lookup means: callers that fetch
// external content pass false and refuse, while callers whose dialer re-checks the
// address it actually connects to pass true and keep the name.
func validateResolvedHost(ctx context.Context, host string, failOnLookupErr bool) bool {
	if ip := net.ParseIP(host); ip != nil {
		return !blockedIP(ip)
	}
	if IsInternalHost(host) {
		return false
	}

	lookupCtx, cancel := context.WithTimeout(ctx, externalResolveTimeout)
	defer cancel()
	addresses, err := lookupIPAddr(lookupCtx, host)
	if err != nil {
		return failOnLookupErr
	}
	for _, address := range addresses {
		if blockedIP(address.IP) {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Endpoint & URL verification
// ---------------------------------------------------------------------------

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
	if host == "" || !validateResolvedHost(ctx, host, false) {
		return nil, errKnowledgeURLBlocked
	}
	return parsed, nil
}

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
	if !validateResolvedHost(ctx, host, false) {
		return nil, errExternalMCPURLBlocked
	}
	return parsed, nil
}

// IsAllowedExternalMCPEndpoint checks the shape of one external MCP endpoint:
// https, no credentials, no fragment, the default https port, and a host that is
// neither internal nor resolves into the internal network. A failed lookup keeps
// the name, because the MCP client re-checks the address it actually dials.
func IsAllowedExternalMCPEndpoint(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Fragment != "" {
		return false
	}
	host := parsed.Hostname()
	if host == "" {
		return false
	}
	// Internal services listen on their own ports, so https is pinned to 443.
	if port := parsed.Port(); port != "" && port != "443" {
		return false
	}
	return validateResolvedHost(context.Background(), host, true)
}

// ValidateExternalMCPServerURLsWithHook shows every external MCP endpoint to the
// deployment's hook at validation time, so a script can refuse the room or vet the
// endpoints out of band. It must run after the built-in endpoint checks, which are
// the ones that refuse an unusable address; here only the hook can refuse.
func ValidateExternalMCPServerURLsWithHook(ctx context.Context, roomID string, servers map[string]*plugnmeet.ExternalMCPServerConfig, hook ExternalURLHookRunner) error {
	if hook == nil || len(servers) == 0 {
		return nil
	}

	for _, key := range sortedServerKeys(servers) {
		cfg := servers[key]
		if cfg == nil {
			continue
		}
		if _, err := hook(ctx, &hooks.ExternalUrlHookData{
			Stage:     hooks.ExternalUrlHookStageValidate,
			Type:      hooks.ExternalUrlHookTypeMCP,
			RoomId:    roomID,
			ServerKey: key,
			Url:       strings.TrimSpace(cfg.GetUrl()),
		}); err != nil {
			return err
		}
	}

	return nil
}

// IsAllowedCustomProviderEndpoint checks the shape of a custom provider gateway
// endpoint: https, no credentials, no query and no fragment, a valid port, and a
// host that does not point into the internal network. Custom https ports stay
// allowed, because a self-hosted gateway usually listens on one.
func IsAllowedCustomProviderEndpoint(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" {
		return false
	}
	host := parsed.Hostname()
	if host == "" {
		return false
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return false
		}
	}

	// Whether the host is internal or resolves into the internal network is
	// decided once, by the shared host check.
	return validateResolvedHost(context.Background(), host, true)
}

// hasAllowedPrefix reports whether rawURL is an https page under one of the approved URL prefixes.
// Prefixes are compared by host, port and path boundary, never as raw strings, so neither a confusing suffix nor a sibling
// path can pass.
func hasAllowedPrefix(rawURL string, allowed []string) bool {
	target, ok := parseAllowedHTTPSURL(rawURL)
	if !ok {
		return false
	}
	for _, prefix := range allowed {
		p, ok := parseAllowedHTTPSURL(prefix)
		if !ok {
			continue
		}
		if !strings.EqualFold(target.Hostname(), p.Hostname()) {
			continue
		}
		if effectiveHTTPSPort(target) != effectiveHTTPSPort(p) {
			continue
		}
		// A query string carries room data out of the room, so it is only
		// allowed when the host approved that exact query.
		if target.RawQuery != p.RawQuery {
			continue
		}
		if pathUnderPrefix(target, p) {
			return true
		}
	}
	return false
}

// parseAllowedHTTPSURL accepts only a plain https URL without credentials, a
// fragment or a path that could be re-interpreted after normalisation. Raw IP
// literals and internal hostnames are refused, so an allow-list entry can never
// point at the server's own network.
func parseAllowedHTTPSURL(raw string) (*url.URL, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil {
		return nil, false
	}
	if !strings.EqualFold(parsed.Scheme, "https") || parsed.User != nil || parsed.Fragment != "" || parsed.RawFragment != "" {
		return nil, false
	}
	host := parsed.Hostname()
	if host == "" || net.ParseIP(host) != nil || IsInternalHost(host) {
		return nil, false
	}
	if hasBackslashOrDotDot(parsed.Path) || hasBackslashOrDotDot(parsed.RawPath) {
		return nil, false
	}
	return parsed, true
}

// MaxAllowedKnowledgeURLs caps how many prefixes a room may approve.
const MaxAllowedKnowledgeURLs = 20

// CleanAllowedKnowledgeURLs trims, normalises and de-duplicates the host's
// approved knowledge prefixes, dropping every entry that could not be fetched
// safely: non-https, credentials, fragments, raw IP literals, internal hosts
// and non-443 ports.
func CleanAllowedKnowledgeURLs(urls []string) []string {
	cleaned := make([]string, 0, min(len(urls), MaxAllowedKnowledgeURLs))
	seen := make(map[string]struct{}, len(cleaned))
	for _, raw := range urls {
		if len(cleaned) >= MaxAllowedKnowledgeURLs {
			break
		}
		parsed, ok := parseAllowedHTTPSURL(raw)
		if !ok || effectiveHTTPSPort(parsed) != "443" {
			continue
		}
		normalized := parsed.String()
		if _, duplicate := seen[normalized]; duplicate {
			continue
		}
		seen[normalized] = struct{}{}
		cleaned = append(cleaned, normalized)
	}
	return cleaned
}

// knowledgeHostCheck memoises the host verification of one validation run, so a
// room that approves many pages of the same host resolves it only once.
type knowledgeHostCheck map[string]bool

func (c knowledgeHostCheck) allows(ctx context.Context, host string) bool {
	if allowed, seen := c[host]; seen {
		return allowed
	}
	allowed := validateResolvedHost(ctx, host, true)
	c[host] = allowed
	return allowed
}

// ValidateAllowedKnowledgeURLs vets the host-supplied knowledge prefixes of a room
// before they are normalised. CleanAllowedKnowledgeURLs drops every unusable entry,
// so this must run on the raw request to refuse the room instead of silently
// approving nothing. It returns ErrInvalidExternalURL for a built-in refusal and
// the hook's own error when the deployment refuses the room.
func ValidateAllowedKnowledgeURLs(ctx context.Context, roomID string, urls []string, hook ExternalURLHookRunner) error {
	if len(urls) > MaxAllowedKnowledgeURLs {
		return ErrInvalidExternalURL
	}

	hosts := make(knowledgeHostCheck, len(urls))
	for _, raw := range urls {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			// an unset entry approves nothing, so it is dropped rather than refused
			continue
		}
		parsed, ok := parseAllowedHTTPSURL(trimmed)
		if !ok || effectiveHTTPSPort(parsed) != "443" || !hosts.allows(ctx, parsed.Hostname()) {
			return ErrInvalidExternalURL
		}
		if hook == nil {
			continue
		}
		if _, err := hook(ctx, &hooks.ExternalUrlHookData{
			Stage:       hooks.ExternalUrlHookStageValidate,
			Type:        hooks.ExternalUrlHookTypeKnowledge,
			RoomId:      roomID,
			Url:         parsed.String(),
			AllowedUrls: urls,
		}); err != nil {
			return err
		}
	}

	return nil
}

// allowedKnowledgeContentTypes are the document types htmlToText can render.
// Everything else (images, audio, video, PDFs, binary blobs) is refused before
// the body is read.
var allowedKnowledgeContentTypes = map[string]struct{}{
	"text/html":             {},
	"text/plain":            {},
	"text/markdown":         {},
	"text/xml":              {},
	"text/csv":              {},
	"application/xhtml+xml": {},
	"application/json":      {},
	"application/xml":       {},
	"application/ld+json":   {},
}

// isAllowedKnowledgeContentType reports whether the response may be read. An
// absent Content-Type is accepted, because some documentation servers omit it.
func isAllowedKnowledgeContentType(header string) bool {
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(header, ";")[0]))
	if mediaType == "" {
		return true
	}
	_, ok := allowedKnowledgeContentTypes[mediaType]
	return ok
}

func hasBackslashOrDotDot(rawPath string) bool {
	if strings.Contains(rawPath, `\`) {
		return true
	}
	for _, segment := range strings.Split(rawPath, "/") {
		if segment == ".." {
			return true
		}
		if decoded, err := url.PathUnescape(segment); err == nil && decoded == ".." {
			return true
		}
	}
	return false
}

func effectiveHTTPSPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	return "443"
}

// pathUnderPrefix compares cleaned paths on segment boundaries, so
// https://example.com/kb never matches https://example.com/kb-secret.
func pathUnderPrefix(target, prefix *url.URL) bool {
	cleanPrefix := path.Clean("/" + prefix.Path)
	cleanTarget := path.Clean("/" + target.Path)
	if cleanPrefix == "/" {
		return true
	}
	return cleanTarget == cleanPrefix || strings.HasPrefix(cleanTarget, cleanPrefix+"/")
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
			if len(via) >= maxKnowledgeRedirects {
				return errKnowledgeURLBlocked
			}
			_, err := guard.validate(req.Context(), req.URL.String())
			return err
		},
	}
}

// withKnowledgeRedirectPolicy keeps every redirect on the approved prefixes
// without mutating the shared client.
func withKnowledgeRedirectPolicy(client *http.Client, guard urlGuard, allowed []string) *http.Client {
	if len(allowed) == 0 {
		return client
	}
	next := client.CheckRedirect
	clone := *client
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxKnowledgeRedirects {
			return errKnowledgeURLBlocked
		}
		if !hasAllowedPrefix(req.URL.String(), allowed) {
			return errKnowledgeURLBlocked
		}
		if next != nil {
			return next(req, via)
		}
		_, err := guard.validate(req.Context(), req.URL.String())
		return err
	}
	return &clone
}

// ---------------------------------------------------------------------------
// Untrusted external text & metadata sanitisation
// ---------------------------------------------------------------------------

// reExternalMCPFrameBreaker matches the frames this package writes, so an
// external document can never open or close one itself.
var reExternalMCPFrameBreaker = regexp.MustCompile(`(?i)\[\s*/?\s*(UNTRUSTED EXTERNAL DOCUMENT|HOST INSTRUCTION|SYSTEM CONTEXT)[^\]]*\]`)

// sanitizeExternalMCPText strips control characters, defuses frame breakers and
// truncates to the model-facing MCP budget.
func sanitizeExternalMCPText(text string) (string, bool) {
	return sanitizeUntrustedExternalText(text, MaxExternalMCPOutputChars)
}

// sanitizeUntrustedExternalText neutralises text coming from outside the host
// and truncates it to maxChars runes. A non-positive maxChars means no limit.
func sanitizeUntrustedExternalText(text string, maxChars int) (string, bool) {
	text = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r < 0x20 || r == 0x7f:
			return -1
		}
		return r
	}, text)
	text = reExternalMCPFrameBreaker.ReplaceAllString(text, "[removed]")
	text = strings.TrimSpace(text)

	truncated := false
	if maxChars > 0 && utf8.RuneCountInString(text) > maxChars {
		text = truncateRunes(text, maxChars)
		truncated = true
	}
	return text, truncated
}

// sanitizeKnowledgeMeta renders one header line as a single line: a metadata
// value must never be able to start a new line of the frame.
func sanitizeKnowledgeMeta(value string) string {
	cleaned := reExternalMCPFrameBreaker.ReplaceAllString(strings.TrimSpace(value), " ")
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	return strings.NewReplacer("[", "(", "]", ")").Replace(cleaned)
}

func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}
