package models

import "testing"

// The guard itself is covered by the shared mcp tests; here we only pin the
// delegation, using endpoints that need no DNS lookup.
func TestIsAllowedAgentCustomProviderEndpointDelegatesToTheSharedGuard(t *testing.T) {
	for _, endpoint := range []string{
		"http://gw.example.com/v1",
		"https://localhost/v1",
		"https://api.localhost/v1",
		"https://gateway.local/v1",
		"https://llm.internal/v1",
		"https://intranet/v1",
		"https://127.0.0.1/v1",
		"https://[::1]/v1",
		"https://10.1.2.3/v1",
		"https://169.254.169.254/v1",
		"https://u:p@gw.example.com/v1",
		"https://gw.example.com/v1?trace=1",
		"https://gw.example.com/v1#frag",
		"https://gw.example.com:0/v1",
	} {
		if isAllowedAgentCustomProviderEndpoint(endpoint) {
			t.Errorf("%q must be rejected", endpoint)
		}
	}

	// A public gateway on a custom https port stays allowed.
	if !isAllowedAgentCustomProviderEndpoint("https://93.184.216.34:8443/v1") {
		t.Error("a public gateway on a custom https port must be allowed")
	}
}
