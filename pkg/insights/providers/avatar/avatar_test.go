package avatar

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights/media"
)

const (
	testAPIKey       = "test-api-key"
	testAvatarID     = "avatar-123"
	testLiveKitURL   = "wss://livekit.example.com"
	testLiveKitToken = "livekit-token-abc"
)

// newTestConfig returns a StartConfig pointing at the test server.
func newTestConfig(provider config.ProviderType, endpoint string) StartConfig {
	return StartConfig{
		Provider:     provider,
		APIKey:       testAPIKey,
		AvatarID:     testAvatarID,
		Endpoint:     endpoint,
		LiveKitURL:   testLiveKitURL,
		LiveKitToken: testLiveKitToken,
	}
}

// decodeBody decodes the recorded request body into a generic map.
func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode request body: %v", err)
	}
	return body
}

func TestStartSessionBeyondPresence(t *testing.T) {
	var gotPath, gotAPIKey, gotContentType string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAPIKey = r.Header.Get("x-api-key")
		gotContentType = r.Header.Get("Content-Type")
		gotBody = decodeBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"bp-session-1"}`))
	}))
	defer srv.Close()

	// The trailing slash must be trimmed from the endpoint override.
	sess, err := StartSession(context.Background(), newTestConfig(config.ProviderBeyondPresence, srv.URL+"/"))
	if err != nil {
		t.Fatalf("StartSession returned error: %v", err)
	}

	if gotPath != "/v1/sessions" {
		t.Errorf("unexpected path: %q", gotPath)
	}
	if gotAPIKey != testAPIKey {
		t.Errorf("unexpected x-api-key header: %q", gotAPIKey)
	}
	if gotContentType != "application/json" {
		t.Errorf("unexpected Content-Type header: %q", gotContentType)
	}
	// Both the current and the legacy transport field names must be present so
	// either version of the /v1/sessions schema is accepted.
	wantBody := map[string]any{
		"avatar_id":      testAvatarID,
		"transport":      "livekit",
		"transport_type": "livekit",
		"url":            testLiveKitURL,
		"livekit_url":    testLiveKitURL,
		"token":          testLiveKitToken,
		"livekit_token":  testLiveKitToken,
	}
	for key, want := range wantBody {
		if gotBody[key] != want {
			t.Errorf("body[%q] = %v, want %v", key, gotBody[key], want)
		}
	}
	if len(gotBody) != len(wantBody) {
		t.Errorf("unexpected body fields: %v", gotBody)
	}

	if sess.Provider() != config.ProviderBeyondPresence {
		t.Errorf("Provider() = %q", sess.Provider())
	}
	if sess.SessionID() != "bp-session-1" {
		t.Errorf("SessionID() = %q", sess.SessionID())
	}
	if sess.SampleRate() != 16000 || sess.SampleRate() != media.DefaultSampleRate16k {
		t.Errorf("SampleRate() = %d, want 16000", sess.SampleRate())
	}
	if err := sess.Close(context.Background()); err != nil {
		t.Errorf("Close() returned error: %v", err)
	}
}

func TestStartSessionAnam(t *testing.T) {
	var gotPath, gotAuth, gotContentType string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		gotBody = decodeBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sessionId":"anam-session-1"}`))
	}))
	defer srv.Close()

	sess, err := StartSession(context.Background(), newTestConfig(config.ProviderAnam, srv.URL))
	if err != nil {
		t.Fatalf("StartSession returned error: %v", err)
	}

	if gotPath != "/v1/engine/session" {
		t.Errorf("unexpected path: %q", gotPath)
	}
	if gotAuth != "Bearer "+testAPIKey {
		t.Errorf("unexpected Authorization header: %q", gotAuth)
	}
	if gotContentType != "application/json" {
		t.Errorf("unexpected Content-Type header: %q", gotContentType)
	}

	persona, ok := gotBody["personaConfig"].(map[string]any)
	if !ok {
		t.Fatalf("personaConfig missing or wrong type: %v", gotBody["personaConfig"])
	}
	wantPersona := map[string]any{
		"type":     "ephemeral",
		"avatarId": testAvatarID,
		"llmId":    "CUSTOMER_CLIENT_V1",
	}
	for key, want := range wantPersona {
		if persona[key] != want {
			t.Errorf("personaConfig[%q] = %v, want %v", key, persona[key], want)
		}
	}

	environment, ok := gotBody["environment"].(map[string]any)
	if !ok {
		t.Fatalf("environment missing or wrong type: %v", gotBody["environment"])
	}
	wantEnv := map[string]any{
		"livekitUrl":   testLiveKitURL,
		"livekitToken": testLiveKitToken,
	}
	for key, want := range wantEnv {
		if environment[key] != want {
			t.Errorf("environment[%q] = %v, want %v", key, environment[key], want)
		}
	}

	if sess.Provider() != config.ProviderAnam {
		t.Errorf("Provider() = %q", sess.Provider())
	}
	if sess.SessionID() != "anam-session-1" {
		t.Errorf("SessionID() = %q", sess.SessionID())
	}
	if sess.SampleRate() != 24000 || sess.SampleRate() != media.DefaultOutputSampleRate24k {
		t.Errorf("SampleRate() = %d, want 24000", sess.SampleRate())
	}
	if err := sess.Close(context.Background()); err != nil {
		t.Errorf("Close() returned error: %v", err)
	}
}

func TestStartSessionSimli(t *testing.T) {
	var paths []string
	var apiKeys []string
	var bodies []map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		apiKeys = append(apiKeys, r.Header.Get("x-simli-api-key"))
		bodies = append(bodies, decodeBody(t, r))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/compose/token":
			_, _ = w.Write([]byte(`{"session_token":"simli-session-token"}`))
		case "/integrations/livekit/agents":
			_, _ = w.Write([]byte(`{"session_id":"simli-session-1"}`))
		default:
			t.Errorf("unexpected path: %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	sess, err := StartSession(context.Background(), newTestConfig(config.ProviderSimli, srv.URL))
	if err != nil {
		t.Fatalf("StartSession returned error: %v", err)
	}

	wantPaths := []string{"/compose/token", "/integrations/livekit/agents"}
	if len(paths) != len(wantPaths) {
		t.Fatalf("unexpected request paths: %v", paths)
	}
	for i, want := range wantPaths {
		if paths[i] != want {
			t.Errorf("request %d path = %q, want %q", i, paths[i], want)
		}
		if apiKeys[i] != testAPIKey {
			t.Errorf("request %d x-simli-api-key = %q", i, apiKeys[i])
		}
	}

	tokenBody := bodies[0]
	wantTokenBody := map[string]any{
		"faceId":           testAvatarID,
		"handleSilence":    true,
		"maxSessionLength": float64(3600),
		"maxIdleTime":      float64(300),
	}
	for key, want := range wantTokenBody {
		if tokenBody[key] != want {
			t.Errorf("/compose/token body[%q] = %v, want %v", key, tokenBody[key], want)
		}
	}

	joinBody := bodies[1]
	wantJoinBody := map[string]any{
		"session_token": "simli-session-token",
		"livekit_url":   testLiveKitURL,
		"livekit_token": testLiveKitToken,
	}
	for key, want := range wantJoinBody {
		if joinBody[key] != want {
			t.Errorf("/integrations/livekit/agents body[%q] = %v, want %v", key, joinBody[key], want)
		}
	}

	if sess.Provider() != config.ProviderSimli {
		t.Errorf("Provider() = %q", sess.Provider())
	}
	if sess.SessionID() != "simli-session-1" {
		t.Errorf("SessionID() = %q", sess.SessionID())
	}
	if sess.SampleRate() != 16000 || sess.SampleRate() != media.DefaultSampleRate16k {
		t.Errorf("SampleRate() = %d, want 16000", sess.SampleRate())
	}
	if err := sess.Close(context.Background()); err != nil {
		t.Errorf("Close() returned error: %v", err)
	}
}

func TestStartSessionSimliFallsBackToSessionToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/compose/token":
			_, _ = w.Write([]byte(`{"session_token":"simli-session-token"}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	sess, err := StartSession(context.Background(), newTestConfig(config.ProviderSimli, srv.URL))
	if err != nil {
		t.Fatalf("StartSession returned error: %v", err)
	}
	if sess.SessionID() != "simli-session-token" {
		t.Errorf("SessionID() = %q, want the session token fallback", sess.SessionID())
	}
}

func TestStartSessionDefaultsEndpointWhenOverrideIsEmpty(t *testing.T) {
	tests := []struct {
		provider config.ProviderType
		want     string
	}{
		{config.ProviderBeyondPresence, defaultBeyondPresenceEndpoint},
		{config.ProviderAnam, defaultAnamEndpoint},
		{config.ProviderSimli, defaultSimliEndpoint},
		{config.ProviderTavus, defaultTavusEndpoint},
	}

	for _, tt := range tests {
		t.Run(string(tt.provider), func(t *testing.T) {
			cfg := newTestConfig(tt.provider, "")
			// Uses a stub client so the default endpoint is only inspected, not dialed.
			var gotURL string
			cfg.HTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				gotURL = req.URL.Scheme + "://" + req.URL.Host + req.URL.Path
				if req.URL.Host == "" {
					t.Fatalf("unexpected request url: %s", req.URL.String())
				}
				return nil, errStopRoundTrip
			})}

			if _, err := StartSession(context.Background(), cfg); err == nil {
				t.Fatal("expected an error from the stub transport")
			}
			// The request must go to the default endpoint of the provider; a
			// default that carries a version path is a prefix of it.
			if !strings.HasPrefix(gotURL, tt.want) {
				t.Errorf("request url = %q, want it to start with %q", gotURL, tt.want)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type roundTripError string

func (e roundTripError) Error() string { return string(e) }

const errStopRoundTrip = roundTripError("stub transport: stop")

func TestStartSessionValidation(t *testing.T) {
	valid := newTestConfig(config.ProviderBeyondPresence, "https://example.com")

	tests := []struct {
		name   string
		mutate func(*StartConfig)
		want   string
	}{
		{"missing api key", func(c *StartConfig) { c.APIKey = "" }, "api key is required"},
		{"missing avatar id", func(c *StartConfig) { c.AvatarID = "" }, "avatar id is required"},
		{"missing livekit url", func(c *StartConfig) { c.LiveKitURL = "" }, "livekit url is required"},
		{"missing livekit token", func(c *StartConfig) { c.LiveKitToken = "" }, "livekit token is required"},
		{"unsupported provider", func(c *StartConfig) { c.Provider = config.ProviderOpenAI }, "unsupported provider"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.mutate(&cfg)
			sess, err := StartSession(context.Background(), cfg)
			if err == nil {
				t.Fatalf("expected an error, got session %v", sess)
			}
			if sess != nil {
				t.Errorf("expected a nil session, got %v", sess)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestStartSessionNon2xxError(t *testing.T) {
	tests := []struct {
		name     string
		provider config.ProviderType
		status   int
		body     string
		wantIn   []string
	}{
		{
			name:     "beyond_presence",
			provider: config.ProviderBeyondPresence,
			status:   http.StatusUnauthorized,
			body:     `{"message":"invalid api key"}`,
			wantIn:   []string{"beyond_presence", "401", "invalid api key"},
		},
		{
			name:     "anam",
			provider: config.ProviderAnam,
			status:   http.StatusBadRequest,
			body:     `{"message":"bad persona"}`,
			wantIn:   []string{"anam", "400", "bad persona"},
		},
		{
			name:     "simli",
			provider: config.ProviderSimli,
			status:   http.StatusInternalServerError,
			body:     `{"message":"simli boom"}`,
			wantIn:   []string{"simli", "500", "simli boom"},
		},
		{
			name:     "tavus",
			provider: config.ProviderTavus,
			status:   http.StatusForbidden,
			body:     `{"message":"invalid pal"}`,
			wantIn:   []string{"tavus", "403", "invalid pal"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			sess, err := StartSession(context.Background(), newTestConfig(tt.provider, srv.URL))
			if err == nil {
				t.Fatalf("expected an error, got session %v", sess)
			}
			for _, want := range tt.wantIn {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to contain %q", err, want)
				}
			}
		})
	}
}

func TestStartSessionErrorBodyIsTruncated(t *testing.T) {
	long := strings.Repeat("x", 1000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(long))
	}))
	defer srv.Close()

	_, err := StartSession(context.Background(), newTestConfig(config.ProviderBeyondPresence, srv.URL))
	if err == nil {
		t.Fatal("expected an error")
	}
	if len(err.Error()) > len(long)/2 {
		t.Errorf("error message was not truncated: %d bytes", len(err.Error()))
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("error = %q, want the status code", err)
	}
}

func TestStartSessionMissingResponseFields(t *testing.T) {
	tests := []struct {
		name     string
		provider config.ProviderType
		body     string
		want     string
	}{
		{"beyond_presence missing id", config.ProviderBeyondPresence, `{}`, "missing id"},
		{"anam missing sessionId", config.ProviderAnam, `{}`, "missing sessionId"},
		{"simli missing session_token", config.ProviderSimli, `{}`, "missing session_token"},
		{"tavus missing conversation_id", config.ProviderTavus, `{}`, "missing conversation_id"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			if _, err := StartSession(context.Background(), newTestConfig(tt.provider, srv.URL)); err == nil {
				t.Fatal("expected an error")
			} else if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

// recordedLiveKitURL returns the LiveKit URL carried by a provider request
// body. Anam nests it in the environment object, the others send it flat.
func recordedLiveKitURL(body map[string]any) (string, bool) {
	if environment, ok := body["environment"].(map[string]any); ok {
		body = environment
	}
	for _, key := range []string{"livekit_url", "livekitUrl"} {
		if value, ok := body[key].(string); ok {
			return value, true
		}
	}
	return "", false
}

// The providers only accept a websocket room URL, so StartSession must convert
// an http(s) URL to ws(s) and trim a trailing slash.
func TestStartSessionNormalizesLiveKitURL(t *testing.T) {
	urlTests := []struct {
		name string
		in   string
		want string
	}{
		{"https becomes wss and the trailing slash is trimmed", "https://livekit.example.com/", "wss://livekit.example.com"},
		{"http becomes ws", "http://localhost:7880", "ws://localhost:7880"},
	}

	for _, provider := range []config.ProviderType{
		config.ProviderBeyondPresence,
		config.ProviderAnam,
		config.ProviderSimli,
	} {
		for _, tt := range urlTests {
			t.Run(string(provider)+"/"+tt.name, func(t *testing.T) {
				var gotURL string
				var gotURLSet bool

				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if value, ok := recordedLiveKitURL(decodeBody(t, r)); ok {
						gotURL, gotURLSet = value, true
					}
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/v1/sessions":
						_, _ = w.Write([]byte(`{"id":"bp-session-1"}`))
					case "/v1/engine/session":
						_, _ = w.Write([]byte(`{"sessionId":"anam-session-1"}`))
					case "/compose/token":
						_, _ = w.Write([]byte(`{"session_token":"simli-session-token"}`))
					default:
						_, _ = w.Write([]byte(`{"session_id":"simli-session-1"}`))
					}
				}))
				defer srv.Close()

				cfg := newTestConfig(provider, srv.URL)
				cfg.LiveKitURL = tt.in
				if _, err := StartSession(context.Background(), cfg); err != nil {
					t.Fatalf("StartSession returned error: %v", err)
				}
				if !gotURLSet {
					t.Fatal("no request carried a livekit url")
				}
				if gotURL != tt.want {
					t.Errorf("livekit url = %q, want %q", gotURL, tt.want)
				}
			})
		}
	}
}

func TestNormalizeLiveKitURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"https with slash", "https://livekit.example.com/", "wss://livekit.example.com"},
		{"http with port", "http://localhost:7880", "ws://localhost:7880"},
		{"wss is kept", "wss://livekit.example.com", "wss://livekit.example.com"},
		{"ws is kept", "ws://localhost:7880", "ws://localhost:7880"},
		{"no scheme gets wss", "livekit.example.com", "wss://livekit.example.com"},
		{"whitespace is trimmed", "  wss://livekit.example.com/  ", "wss://livekit.example.com"},
		{"scheme is case insensitive", "HTTPS://LiveKit.Example.com/", "wss://LiveKit.Example.com"},
		{"empty stays empty", "   ", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeLiveKitURL(tt.in); got != tt.want {
				t.Errorf("normalizeLiveKitURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// A custom AuthHeader replaces the provider default, so a gateway that expects
// another header receives the key there and nowhere else.
func TestStartSessionCustomAuthHeader(t *testing.T) {
	tests := []struct {
		name       string
		provider   config.ProviderType
		header     string
		apiKey     string
		wantHeader string
		wantValue  string
		defaults   []string
	}{
		{
			name:       "beyond_presence gateway header",
			provider:   config.ProviderBeyondPresence,
			header:     "x-gateway-key",
			apiKey:     testAPIKey,
			wantHeader: "x-gateway-key",
			wantValue:  testAPIKey,
			defaults:   []string{"x-api-key"},
		},
		{
			name:       "beyond_presence authorization gains the bearer scheme",
			provider:   config.ProviderBeyondPresence,
			header:     "Authorization",
			apiKey:     testAPIKey,
			wantHeader: "Authorization",
			wantValue:  "Bearer " + testAPIKey,
			defaults:   []string{"x-api-key"},
		},
		{
			// an already prefixed key is forwarded as-is
			name:       "anam keeps an existing bearer prefix",
			provider:   config.ProviderAnam,
			header:     "Authorization",
			apiKey:     "Bearer " + testAPIKey,
			wantHeader: "Authorization",
			wantValue:  "Bearer " + testAPIKey,
			defaults:   []string{},
		},
		{
			name:       "anam custom header sends the bare key",
			provider:   config.ProviderAnam,
			header:     "x-anam-key",
			apiKey:     testAPIKey,
			wantHeader: "x-anam-key",
			wantValue:  testAPIKey,
			defaults:   []string{"Authorization"},
		},
		{
			name:       "simli custom header",
			provider:   config.ProviderSimli,
			header:     "x-simli-gateway",
			apiKey:     testAPIKey,
			wantHeader: "x-simli-gateway",
			wantValue:  testAPIKey,
			defaults:   []string{"x-simli-api-key"},
		},
		{
			name:       "tavus custom header",
			provider:   config.ProviderTavus,
			header:     "x-tavus-gateway",
			apiKey:     testAPIKey,
			wantHeader: "x-tavus-gateway",
			wantValue:  testAPIKey,
			defaults:   []string{"x-api-key"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotValues []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotValues = append(gotValues, r.Header.Get(tt.wantHeader))
				for _, header := range tt.defaults {
					if value := r.Header.Get(header); value != "" {
						t.Errorf("default header %q = %q, want it unused", header, value)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				switch tt.provider {
				case config.ProviderBeyondPresence:
					_, _ = w.Write([]byte(`{"id":"bp-session-1"}`))
				case config.ProviderAnam:
					_, _ = w.Write([]byte(`{"sessionId":"anam-session-1"}`))
				case config.ProviderSimli:
					if r.URL.Path == "/compose/token" {
						_, _ = w.Write([]byte(`{"session_token":"simli-session-token"}`))
						return
					}
					_, _ = w.Write([]byte(`{"session_id":"simli-session-1"}`))
				case config.ProviderTavus:
					_, _ = w.Write([]byte(`{"conversation_id":"tavus-conversation-1"}`))
				}
			}))
			defer srv.Close()

			cfg := newTestConfig(tt.provider, srv.URL)
			cfg.AuthHeader = "  " + tt.header + "  "
			cfg.APIKey = tt.apiKey
			if _, err := StartSession(context.Background(), cfg); err != nil {
				t.Fatalf("StartSession returned error: %v", err)
			}

			wantRequests := 1
			if tt.provider == config.ProviderSimli {
				wantRequests = 2
			}
			if len(gotValues) != wantRequests {
				t.Fatalf("requests = %d, want %d", len(gotValues), wantRequests)
			}
			for i, value := range gotValues {
				if value != tt.wantValue {
					t.Errorf("request %d %s = %q, want %q", i, tt.wantHeader, value, tt.wantValue)
				}
			}
		})
	}
}

func TestStartSessionContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"bp-session-1"}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := StartSession(ctx, newTestConfig(config.ProviderBeyondPresence, srv.URL)); err == nil {
		t.Fatal("expected a context error")
	}
}
