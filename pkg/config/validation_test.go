package config

import (
	"testing"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

func TestValidateInsightsConfig(t *testing.T) {
	insightsConfig := func(provider ProviderType, options map[string]interface{}) *InsightsConfig {
		return &InsightsConfig{
			Enabled: true,
			Providers: map[ProviderType][]ProviderAccount{
				ProviderOpenAI: {{ID: "account-1"}},
				ProviderGoogle: {{ID: "account-1"}},
			},
			Services: map[insights.ServiceType]*ServiceConfig{
				insights.ServiceTypeAIAgent: {Provider: provider, ID: "account-1", Options: options},
			},
		}
	}

	tests := []struct {
		name    string
		config  *InsightsConfig
		wantErr bool
	}{
		{"nil config", nil, false},
		{"disabled insights", func() *InsightsConfig {
			cfg := insightsConfig(ProviderOpenAI, map[string]interface{}{"realtime_model": "gpt-live-1"})
			cfg.Enabled = false
			return cfg
		}(), false},
		{"no ai_agent service", &InsightsConfig{
			Enabled:   true,
			Providers: map[ProviderType][]ProviderAccount{ProviderOpenAI: {{ID: "account-1"}}},
		}, false},
		{"non live model", insightsConfig(ProviderOpenAI, map[string]interface{}{"realtime_model": "gpt-realtime-2.1-mini"}), false},
		{"live model without delegation", insightsConfig(ProviderOpenAI, map[string]interface{}{"realtime_model": "gpt-live-1"}), true},
		{"live_model option without delegation", insightsConfig(ProviderOpenAI, map[string]interface{}{"live_model": "gpt-live-1"}), true},
		{"live model with delegation", insightsConfig(ProviderOpenAI, map[string]interface{}{
			"realtime_model":   "gpt-live-1",
			"delegation_model": "gpt-5.4-mini",
		}), false},
		{"other provider is not validated", insightsConfig(ProviderGoogle, map[string]interface{}{"realtime_model": "gpt-live-1"}), false},
		{"unknown provider account", &InsightsConfig{
			Enabled:   true,
			Providers: map[ProviderType][]ProviderAccount{ProviderOpenAI: {{ID: "another-account"}}},
			Services: map[insights.ServiceType]*ServiceConfig{
				insights.ServiceTypeAIAgent: {Provider: ProviderOpenAI, ID: "account-1"},
			},
		}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateInsightsConfig(tt.config)
			if tt.wantErr && err == nil {
				t.Fatal("expected an error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateAppConfigValidatesInsights(t *testing.T) {
	appCnf := &AppConfig{
		LivekitInfo: LivekitInfo{Host: "http://127.0.0.1:7880"},
		NatsInfo:    NatsInfo{NatsUrls: []string{"nats://127.0.0.1:4222"}},
		Insights: &InsightsConfig{
			Enabled: true,
			Providers: map[ProviderType][]ProviderAccount{
				ProviderOpenAI: {{ID: "account-1"}},
			},
			Services: map[insights.ServiceType]*ServiceConfig{
				insights.ServiceTypeAIAgent: {
					Provider: ProviderOpenAI,
					ID:       "account-1",
					Options:  map[string]interface{}{"realtime_model": "gpt-live-1"},
				},
			},
		},
	}

	if err := validateAppConfig(appCnf); err == nil {
		t.Fatal("expected the missing delegation_model to fail validation")
	}
}
