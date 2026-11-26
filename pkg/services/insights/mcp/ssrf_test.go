package mcp

import (
	"context"
	"net"
	"testing"
)

// stubResolvedHosts replaces the shared DNS lookup for one test.
func stubResolvedHosts(t *testing.T, addresses []net.IPAddr, err error) {
	t.Helper()

	previous := lookupIPAddr
	lookupIPAddr = func(context.Context, string) ([]net.IPAddr, error) {
		return addresses, err
	}
	t.Cleanup(func() { lookupIPAddr = previous })
}

func TestIsAllowedCustomProviderEndpointRejectsInternalHosts(t *testing.T) {
	stubResolvedHosts(t, nil, &net.DNSError{Err: "no such host", Name: "gw.example.com", IsNotFound: true})

	for _, endpoint := range []string{
		"https://localhost/v1",
		"https://api.localhost/v1",
		"https://gateway.local/v1",
		"https://llm.internal/v1",
		"https://intranet/v1",
		"https://127.0.0.1/v1",
		"https://[::1]/v1",
		"https://10.1.2.3/v1",
		"https://100.64.1.1/v1",
		"https://169.254.169.254/v1",
		"https://u:p@gw.example.com/v1",
		"https://gw.example.com:0/v1",
	} {
		if IsAllowedCustomProviderEndpoint(endpoint) {
			t.Errorf("%q must be rejected", endpoint)
		}
	}
}

func TestIsAllowedCustomProviderEndpointAllowsPublicGatewayOnCustomPort(t *testing.T) {
	stubResolvedHosts(t, []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil)

	// A self-hosted gateway usually listens on its own https port.
	if !IsAllowedCustomProviderEndpoint("https://gw.example.com:8443/v1") {
		t.Error("a public gateway on a custom https port must be allowed")
	}
}

func TestIsAllowedCustomProviderEndpointRejectsHostsResolvingIntoTheInternalNetwork(t *testing.T) {
	tests := map[string]string{
		"loopback": "127.0.0.1",
		"private":  "10.0.0.5",
		"cgnat":    "100.64.0.7",
		"metadata": "169.254.169.254",
		"ula":      "fd00::1",
	}
	for name, ip := range tests {
		t.Run(name, func(t *testing.T) {
			stubResolvedHosts(t, []net.IPAddr{{IP: net.ParseIP(ip)}}, nil)
			if IsAllowedCustomProviderEndpoint("https://gw.example.com/v1") {
				t.Errorf("a host resolving to %s must be rejected", ip)
			}
		})
	}

	// One bad address is enough, even next to a public one.
	t.Run("one of many", func(t *testing.T) {
		stubResolvedHosts(t, []net.IPAddr{
			{IP: net.ParseIP("93.184.216.34")},
			{IP: net.ParseIP("192.168.1.10")},
		}, nil)
		if IsAllowedCustomProviderEndpoint("https://gw.example.com/v1") {
			t.Error("a host that also resolves into the private network must be rejected")
		}
	})
}

func TestIsAllowedCustomProviderEndpointToleratesAnUnresolvableHost(t *testing.T) {
	stubResolvedHosts(t, nil, &net.DNSError{Err: "no such host", Name: "custom.openai.test", IsNotFound: true})

	// The dialer checks the address it connects to, so a resolver that cannot
	// answer must not refuse an otherwise public-looking name.
	if !IsAllowedCustomProviderEndpoint("https://custom.openai.test/v1") {
		t.Error("an unresolvable public hostname must stay allowed")
	}
}

func TestIsInternalHost(t *testing.T) {
	for _, host := range []string{"localhost", "LOCALHOST.", "api.localhost", "gw.local", "llm.internal", "intranet", ""} {
		if !IsInternalHost(host) {
			t.Errorf("%q must be treated as internal", host)
		}
	}
	for _, host := range []string{"example.com", "gw.example.com.", "docs.example.org"} {
		if IsInternalHost(host) {
			t.Errorf("%q must not be treated as internal", host)
		}
	}
}
