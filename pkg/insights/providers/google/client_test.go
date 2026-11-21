package google

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
	"google.golang.org/genai"
)

func testProviderAccount(apiKey string, options map[string]interface{}) *config.ProviderAccount {
	return &config.ProviderAccount{
		Credentials: config.CredentialsConfig{APIKey: apiKey},
		Options:     options,
	}
}

func TestResolveHTTPOptions(t *testing.T) {
	tests := []struct {
		name           string
		accountOptions map[string]interface{}
		serviceOptions map[string]interface{}
		wantNil        bool
		wantAPIVersion string
		wantBaseURL    string
		wantHeaders    map[string]string
	}{
		{
			name:    "returns nil so the SDK default is used when nothing is set",
			wantNil: true,
		},
		{
			name:           "sets the api version from the account",
			accountOptions: map[string]interface{}{"api_version": " v1alpha "},
			wantAPIVersion: "v1alpha",
		},
		{
			name:           "returns nil when the api version is blank",
			accountOptions: map[string]interface{}{"api_version": "   "},
			wantNil:        true,
		},
		{
			name:           "falls back to the service api version",
			serviceOptions: map[string]interface{}{"api_version": " v1beta "},
			wantAPIVersion: "v1beta",
		},
		{
			name:           "prefers the service api version over the account one",
			accountOptions: map[string]interface{}{"api_version": "v1alpha"},
			serviceOptions: map[string]interface{}{"api_version": "v1beta"},
			wantAPIVersion: "v1beta",
		},
		{
			name:           "keeps the account api version when the service one is blank",
			accountOptions: map[string]interface{}{"api_version": "v1alpha"},
			serviceOptions: map[string]interface{}{"api_version": "   "},
			wantAPIVersion: "v1alpha",
		},
		{
			name:           "sets the custom endpoint only",
			accountOptions: map[string]interface{}{"endpoint": " https://gemini.example.com/ "},
			wantBaseURL:    "https://gemini.example.com/",
		},
		{
			name:           "sets a bearer authorization header",
			accountOptions: map[string]interface{}{"auth_header": "authorization"},
			wantHeaders:    map[string]string{"Authorization": "Bearer sk-test"},
		},
		{
			name:           "sets a custom auth header",
			accountOptions: map[string]interface{}{"auth_header": "api-key"},
			wantHeaders:    map[string]string{"api-key": "sk-test"},
		},
		{
			name:           "falls back to the service realtime_auth_header",
			serviceOptions: map[string]interface{}{"realtime_auth_header": "X-Service-Key"},
			wantHeaders:    map[string]string{"X-Service-Key": "sk-test"},
		},
		{
			name:           "prefers the account auth_header over the service one",
			accountOptions: map[string]interface{}{"auth_header": "X-Account-Key"},
			serviceOptions: map[string]interface{}{"realtime_auth_header": "X-Service-Key"},
			wantHeaders:    map[string]string{"X-Account-Key": "sk-test"},
		},
		{
			name: "combines endpoint and auth header",
			accountOptions: map[string]interface{}{
				"endpoint":    "https://gemini.example.com/",
				"auth_header": "api-key",
			},
			wantBaseURL: "https://gemini.example.com/",
			wantHeaders: map[string]string{"api-key": "sk-test"},
		},
		{
			name: "combines api version, endpoint and auth header",
			accountOptions: map[string]interface{}{
				"api_version": "v1beta",
				"endpoint":    "https://gemini.example.com/",
				"auth_header": "api-key",
			},
			wantAPIVersion: "v1beta",
			wantBaseURL:    "https://gemini.example.com/",
			wantHeaders:    map[string]string{"api-key": "sk-test"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &config.ServiceConfig{Options: tt.serviceOptions}
			if tt.serviceOptions == nil {
				service = nil
			}

			got := resolveHTTPOptions(testProviderAccount("sk-test", tt.accountOptions), service)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("resolveHTTPOptions() = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("resolveHTTPOptions() = nil, want HTTP options")
			}

			if got.APIVersion != tt.wantAPIVersion {
				t.Errorf("APIVersion = %q, want %q", got.APIVersion, tt.wantAPIVersion)
			}
			if got.BaseURL != tt.wantBaseURL {
				t.Errorf("BaseURL = %q, want %q", got.BaseURL, tt.wantBaseURL)
			}
			for key, want := range tt.wantHeaders {
				if value := got.Headers.Get(key); value != want {
					t.Errorf("header %q = %q, want %q", key, value, want)
				}
			}
		})
	}
}

// TestNewProviderSendsCustomHeaders verifies the resolved HTTP options reach the
// genai client by inspecting the request it sends to a stub endpoint.
func TestNewProviderSendsCustomHeaders(t *testing.T) {
	var gotPath string
	var gotHeader http.Header

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHeader = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"ok"}],"role":"model"}}]}`))
	}))
	defer server.Close()

	provider, err := NewProvider(
		context.Background(),
		testProviderAccount("sk-test", map[string]interface{}{
			"endpoint":    server.URL,
			"auth_header": "api-key",
		}),
		nil,
		logrus.New().WithField("test", "google-client"),
	)
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	googleProvider, ok := provider.(*GoogleProvider)
	if !ok {
		t.Fatalf("NewProvider() returned %T, want *GoogleProvider", provider)
	}

	_, err = googleProvider.client.Models.GenerateContent(
		context.Background(), "gemini-test", genai.Text("hi"), nil,
	)
	if err != nil {
		t.Fatalf("GenerateContent() error = %v", err)
	}

	if gotPath == "" {
		t.Fatal("stub endpoint did not receive a request")
	}
	if got := gotHeader.Get("api-key"); got != "sk-test" {
		t.Errorf("api-key header = %q, want %q", got, "sk-test")
	}
}
