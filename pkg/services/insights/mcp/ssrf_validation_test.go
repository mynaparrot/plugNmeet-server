package mcp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/hooks"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

func publicHost(t *testing.T) {
	t.Helper()
	stubResolvedHosts(t, []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil)
}

func TestValidateAllowedKnowledgeURLsRejectsUnusablePrefixes(t *testing.T) {
	publicHost(t)

	tests := map[string][]string{
		"http scheme":        {"http://docs.example.com/kb"},
		"raw ip literal":     {"https://1.2.3.4/docs"},
		"loopback host":      {"https://localhost/docs"},
		"internal host":      {"https://intranet/docs"},
		"reserved suffix":    {"https://docs.internal/kb"},
		"non default port":   {"https://docs.example.com:8443/kb"},
		"credentials":        {"https://user:pw@docs.example.com/kb"},
		"fragment":           {"https://docs.example.com/kb#frag"},
		"missing host":       {"https:///kb"},
		"one bad of many":    {"https://docs.example.com/kb", "https://10.0.0.5/kb"},
		"unparsable scheme":  {"docs.example.com/kb"},
		"path traversal":     {"https://docs.example.com/kb/../../etc"},
		"empty url string":   {"https://docs.example.com/kb", "#frag"},
		"backslash in path":  {"https://docs.example.com\\kb"},
		"protocol relative":  {"//docs.example.com/kb"},
		"ipv6 loopback host": {"https://[::1]/kb"},
	}

	for name, urls := range tests {
		t.Run(name, func(t *testing.T) {
			if err := ValidateAllowedKnowledgeURLs(context.Background(), "room-1", urls, nil); !errors.Is(err, ErrInvalidExternalURL) {
				t.Fatalf("err = %v, want %v", err, ErrInvalidExternalURL)
			}
		})
	}

	t.Run("over the room budget", func(t *testing.T) {
		urls := make([]string, 0, MaxAllowedKnowledgeURLs+1)
		for i := 0; i <= MaxAllowedKnowledgeURLs; i++ {
			urls = append(urls, fmt.Sprintf("https://docs.example.com/p%d", i))
		}
		if err := ValidateAllowedKnowledgeURLs(context.Background(), "room-1", urls, nil); !errors.Is(err, ErrInvalidExternalURL) {
			t.Fatalf("err = %v, want %v", err, ErrInvalidExternalURL)
		}
	})
}

func TestValidateAllowedKnowledgeURLsAcceptsPublicPrefixes(t *testing.T) {
	publicHost(t)

	urls := []string{" https://docs.example.com/kb ", "https://docs.example.com/guide/", "", "   "}
	got := make([]string, 0, 2)
	hook := func(_ context.Context, req *hooks.ExternalUrlHookData) (*hooks.ExternalUrlHookData, error) {
		if req.Stage != hooks.ExternalUrlHookStageValidate {
			t.Errorf("stage = %q, want %q", req.Stage, hooks.ExternalUrlHookStageValidate)
		}
		if req.Type != hooks.ExternalUrlHookTypeKnowledge {
			t.Errorf("type = %q, want %q", req.Type, hooks.ExternalUrlHookTypeKnowledge)
		}
		if req.RoomId != "room-7" {
			t.Errorf("room id = %q, want room-7", req.RoomId)
		}
		if strings.Join(req.AllowedUrls, ",") != strings.Join(urls, ",") {
			t.Errorf("allowed urls = %v, want the room's raw list", req.AllowedUrls)
		}
		got = append(got, req.Url)
		return nil, nil
	}

	if err := ValidateAllowedKnowledgeURLs(context.Background(), "room-7", urls, hook); err != nil {
		t.Fatalf("valid prefixes were refused: %v", err)
	}
	// a blank entry approves nothing, so the hook never sees it
	if want := "https://docs.example.com/kb,https://docs.example.com/guide/"; strings.Join(got, ",") != want {
		t.Fatalf("hook saw %v, want %q", got, want)
	}

	if err := ValidateAllowedKnowledgeURLs(context.Background(), "room-7", nil, hook); err != nil {
		t.Fatalf("an empty list must pass: %v", err)
	}
}

func TestValidateAllowedKnowledgeURLsReturnsTheHookRefusal(t *testing.T) {
	publicHost(t)

	wantErr := errors.New("blocked by the deployment")
	hook := func(context.Context, *hooks.ExternalUrlHookData) (*hooks.ExternalUrlHookData, error) {
		return nil, wantErr
	}

	if err := ValidateAllowedKnowledgeURLs(context.Background(), "room-1", []string{"https://docs.example.com/kb"}, hook); !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want the hook's refusal %v", err, wantErr)
	}
}

func TestValidateAllowedKnowledgeURLsRejectsHostsResolvingIntoTheInternalNetwork(t *testing.T) {
	for name, ip := range map[string]string{
		"loopback": "127.0.0.1",
		"private":  "10.0.0.5",
		"cgnat":    "100.64.0.7",
		"metadata": "169.254.169.254",
		"ula":      "fd00::1",
	} {
		t.Run(name, func(t *testing.T) {
			stubResolvedHosts(t, []net.IPAddr{{IP: net.ParseIP(ip)}}, nil)
			if err := ValidateAllowedKnowledgeURLs(context.Background(), "room-1", []string{"https://docs.example.com/kb"}, nil); !errors.Is(err, ErrInvalidExternalURL) {
				t.Fatalf("a host resolving to %s must be refused, got %v", ip, err)
			}
		})
	}

	t.Run("unresolvable host", func(t *testing.T) {
		stubResolvedHosts(t, nil, &net.DNSError{Err: "no such host", Name: "docs.example.com", IsNotFound: true})
		// the guarded client re-checks the address it dials, so a resolver that
		// cannot answer must not refuse an otherwise public-looking name
		if err := ValidateAllowedKnowledgeURLs(context.Background(), "room-1", []string{"https://docs.example.com/kb"}, nil); err != nil {
			t.Fatalf("an unresolvable prefix must stay allowed: %v", err)
		}
	})
}

func TestIsAllowedExternalMCPEndpoint(t *testing.T) {
	publicHost(t)

	for _, raw := range []string{
		"https://mcp.example.com/mcp",
		"https://mcp.example.com:443/mcp",
		"https://8.8.8.8/mcp",
		"  https://mcp.example.com/mcp  ",
	} {
		if !IsAllowedExternalMCPEndpoint(raw) {
			t.Errorf("%q must be allowed", raw)
		}
	}

	for name, raw := range map[string]string{
		"http scheme":      "http://mcp.example.com/mcp",
		"credentials":      "https://user:pw@mcp.example.com/mcp",
		"fragment":         "https://mcp.example.com/mcp#frag",
		"missing host":     "https:///mcp",
		"custom port":      "https://mcp.example.com:8443/mcp",
		"loopback ip":      "https://127.0.0.1/mcp",
		"metadata ip":      "https://169.254.169.254/latest/meta-data",
		"private ip":       "https://10.0.0.5/mcp",
		"ipv6 loopback":    "https://[::1]/mcp",
		"localhost host":   "https://localhost/mcp",
		"reserved suffix":  "https://mcp.internal/mcp",
		"single label":     "https://mcp/mcp",
		"empty url string": "   ",
	} {
		if IsAllowedExternalMCPEndpoint(raw) {
			t.Errorf("%s (%q) must be refused", name, raw)
		}
	}

	t.Run("resolved into the internal network", func(t *testing.T) {
		stubResolvedHosts(t, []net.IPAddr{{IP: net.ParseIP("192.168.1.10")}}, nil)
		if IsAllowedExternalMCPEndpoint("https://mcp.example.com/mcp") {
			t.Error("a host resolving into the private network must be refused")
		}
	})

	t.Run("unresolvable host", func(t *testing.T) {
		stubResolvedHosts(t, nil, &net.DNSError{Err: "no such host", Name: "mcp.example.com", IsNotFound: true})
		if !IsAllowedExternalMCPEndpoint("https://mcp.example.com/mcp") {
			t.Error("an unresolvable public hostname must stay allowed")
		}
	})
}

func TestValidateExternalMCPServerURLsWithHook(t *testing.T) {
	servers := map[string]*plugnmeet.ExternalMCPServerConfig{
		"zeta":  {Url: " https://zeta.example.com/mcp "},
		"alpha": {Url: "https://alpha.example.com/mcp"},
	}

	// no hook configured: nothing to refuse
	if err := ValidateExternalMCPServerURLsWithHook(context.Background(), "room-1", servers, nil); err != nil {
		t.Fatalf("unexpected error without a hook: %v", err)
	}

	var seen []string
	hook := func(_ context.Context, req *hooks.ExternalUrlHookData) (*hooks.ExternalUrlHookData, error) {
		if req.Stage != hooks.ExternalUrlHookStageValidate {
			t.Errorf("stage = %q, want %q", req.Stage, hooks.ExternalUrlHookStageValidate)
		}
		if req.Type != hooks.ExternalUrlHookTypeMCP {
			t.Errorf("type = %q, want %q", req.Type, hooks.ExternalUrlHookTypeMCP)
		}
		if req.RoomId != "room-1" {
			t.Errorf("room id = %q, want room-1", req.RoomId)
		}
		if strings.TrimSpace(req.Url) != req.Url {
			t.Errorf("url = %q, want it trimmed", req.Url)
		}
		seen = append(seen, req.ServerKey)
		return nil, nil
	}
	if err := ValidateExternalMCPServerURLsWithHook(context.Background(), "room-1", servers, hook); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "alpha,zeta"; strings.Join(seen, ",") != want {
		t.Fatalf("hook saw %v, want %q", seen, want)
	}

	wantErr := errors.New("blocked by the deployment")
	refusing := func(context.Context, *hooks.ExternalUrlHookData) (*hooks.ExternalUrlHookData, error) {
		return nil, wantErr
	}
	if err := ValidateExternalMCPServerURLsWithHook(context.Background(), "room-1", servers, refusing); !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want the hook's refusal %v", err, wantErr)
	}
}
