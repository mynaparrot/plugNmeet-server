package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
)

func testProviderAccount(apiKey string, options map[string]interface{}) *config.ProviderAccount {
	return &config.ProviderAccount{
		Credentials: config.CredentialsConfig{APIKey: apiKey},
		Options:     options,
	}
}

func TestResolveAuthHeader(t *testing.T) {
	tests := []struct {
		name    string
		account *config.ProviderAccount
		service *config.ServiceConfig
		want    string
	}{
		{
			name:    "defaults to Authorization without configuration",
			account: testProviderAccount("key", nil),
			want:    "Authorization",
		},
		{
			name: "handles a nil service config",
			want: "Authorization",
		},
		{
			name:    "uses the account auth_header",
			account: testProviderAccount("key", map[string]interface{}{"auth_header": "api-key"}),
			service: &config.ServiceConfig{},
			want:    "api-key",
		},
		{
			name:    "prefers the service realtime_auth_header",
			account: testProviderAccount("key", map[string]interface{}{"auth_header": "api-key"}),
			service: &config.ServiceConfig{Options: map[string]interface{}{"realtime_auth_header": "X-Service-Key"}},
			want:    "X-Service-Key",
		},
		{
			name:    "ignores blank values and trims whitespace",
			account: testProviderAccount("key", map[string]interface{}{"auth_header": "  X-Account-Key  "}),
			service: &config.ServiceConfig{Options: map[string]interface{}{"realtime_auth_header": "   "}},
			want:    "X-Account-Key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveAuthHeader(tt.account, tt.service); got != tt.want {
				t.Errorf("resolveAuthHeader() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewProviderAppliesAuthHeaderAndEndpointToREST(t *testing.T) {
	tests := []struct {
		name           string
		accountOptions map[string]interface{}
		serviceOptions map[string]interface{}
		wantHeaders    map[string]string
		absentHeaders  []string
	}{
		{
			name:        "defaults to bearer authorization",
			wantHeaders: map[string]string{"Authorization": "Bearer sk-test"},
		},
		{
			name:           "uses the account auth_header",
			accountOptions: map[string]interface{}{"auth_header": "api-key"},
			wantHeaders:    map[string]string{"api-key": "sk-test"},
			absentHeaders:  []string{"Authorization"},
		},
		{
			name:           "uses the service realtime_auth_header",
			accountOptions: map[string]interface{}{"auth_header": "api-key"},
			serviceOptions: map[string]interface{}{"realtime_auth_header": "X-Service-Key"},
			wantHeaders:    map[string]string{"X-Service-Key": "sk-test"},
			absentHeaders:  []string{"Authorization", "api-key"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			var gotHeader http.Header

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotHeader = r.Header.Clone()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			defer server.Close()

			accountOptions := map[string]interface{}{"endpoint": server.URL + "/custom/v1"}
			for key, value := range tt.accountOptions {
				accountOptions[key] = value
			}

			provider, err := NewProvider(
				context.Background(),
				testProviderAccount("sk-test", accountOptions),
				&config.ServiceConfig{Options: tt.serviceOptions},
				logrus.New().WithField("test", "openai-client"),
				nil,
			)
			if err != nil {
				t.Fatalf("NewProvider() error = %v", err)
			}

			openAiProvider, ok := provider.(*OpenAIProvider)
			if !ok {
				t.Fatalf("NewProvider() returned %T, want *OpenAIProvider", provider)
			}

			if err := openAiProvider.openAiClient.Get(context.Background(), "/models", nil, nil); err != nil {
				t.Fatalf("REST request failed: %v", err)
			}

			if gotPath != "/custom/v1/models" {
				t.Errorf("request path = %q, want %q", gotPath, "/custom/v1/models")
			}
			for key, want := range tt.wantHeaders {
				if got := gotHeader.Get(key); got != want {
					t.Errorf("header %q = %q, want %q", key, got, want)
				}
			}
			for _, key := range tt.absentHeaders {
				if got := gotHeader.Get(key); got != "" {
					t.Errorf("header %q = %q, want it to be absent", key, got)
				}
			}
		})
	}
}

func TestRealtimeHandshakeUsesResolvedAuthHeader(t *testing.T) {
	tests := []struct {
		name           string
		accountOptions map[string]interface{}
		serviceOptions map[string]interface{}
		wantHeaders    map[string]string
		absentHeaders  []string
	}{
		{
			name:        "defaults to bearer authorization",
			wantHeaders: map[string]string{"Authorization": "Bearer sk-test"},
		},
		{
			name:           "uses the account auth_header",
			accountOptions: map[string]interface{}{"auth_header": "api-key"},
			wantHeaders:    map[string]string{"api-key": "sk-test"},
			absentHeaders:  []string{"Authorization"},
		},
		{
			name:           "prefers the service realtime_auth_header",
			accountOptions: map[string]interface{}{"auth_header": "api-key"},
			serviceOptions: map[string]interface{}{"realtime_auth_header": "X-Service-Key"},
			wantHeaders:    map[string]string{"X-Service-Key": "sk-test"},
			absentHeaders:  []string{"Authorization", "api-key"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc, err := newRealtimeClient(
				testProviderAccount("sk-test", tt.accountOptions),
				&config.ServiceConfig{Options: tt.serviceOptions},
				logrus.New().WithField("test", "openai-realtime"),
				nil,
			)
			if err != nil {
				t.Fatalf("newRealtimeClient() error = %v", err)
			}

			header := http.Header{}
			setOpenAIRealtimeHeaders(header, "sk-test", rc.authHeader())

			for key, want := range tt.wantHeaders {
				if got := header.Get(key); got != want {
					t.Errorf("header %q = %q, want %q", key, got, want)
				}
			}
			for _, key := range tt.absentHeaders {
				if got := header.Get(key); got != "" {
					t.Errorf("header %q = %q, want it to be absent", key, got)
				}
			}
		})
	}
}
