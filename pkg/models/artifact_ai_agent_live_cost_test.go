package models

import (
	"testing"

	"github.com/sirupsen/logrus"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

func liveCostInsightsConfig() *config.InsightsConfig {
	return &config.InsightsConfig{
		Providers: map[config.ProviderType][]config.ProviderAccount{
			config.ProviderOpenAI: {{ID: "account-1"}},
		},
		Services: map[insights.ServiceType]*config.ServiceConfig{
			insights.ServiceTypeAIAgent: {
				Provider: config.ProviderOpenAI,
				ID:       "account-1",
				Options:  map[string]interface{}{"live_model": "gpt-live-1"},
				Pricing: map[string]config.ModelPricing{
					"gpt-live-1":   {InputPricePerMillionTokens: 2, OutputPricePerMillionTokens: 4},
					"avatar_model": {PricePerMinute: 0.6},
				},
			},
		},
	}
}

func newLiveCostArtifactModel(insightsCfg *config.InsightsConfig) *ArtifactModel {
	return &ArtifactModel{
		app: &config.AppConfig{Insights: insightsCfg},
		log: logrus.NewEntry(logrus.New()),
	}
}

// The live estimate must price a running session exactly like its summary
// artifact does.
func TestEstimateAIAgentLiveCost(t *testing.T) {
	m := newLiveCostArtifactModel(liveCostInsightsConfig())
	telemetry := &redisservice.AIAgentSessionTelemetry{
		InputTokens:    1000000,
		OutputTokens:   500000,
		AvatarMillis:   60000,
		AvatarProvider: "simli",
	}

	tokenCost, avatarCost := m.EstimateAIAgentLiveCost(telemetry)
	// one million input at 2 + half a million output at 4
	if !floatEquals(tokenCost, 4) {
		t.Errorf("token cost = %v, want 4", tokenCost)
	}
	if !floatEquals(avatarCost, 0.6) {
		t.Errorf("avatar cost = %v, want the one minute at 0.6", avatarCost)
	}
}

// A room that brought its own keys pays the provider directly, so it carries no
// platform cost.
func TestEstimateAIAgentLiveCostCustomKeys(t *testing.T) {
	m := newLiveCostArtifactModel(liveCostInsightsConfig())

	customProvider := &redisservice.AIAgentSessionTelemetry{
		InputTokens:    1000000,
		OutputTokens:   500000,
		CustomProvider: "openai",
	}
	if tokenCost, avatarCost := m.EstimateAIAgentLiveCost(customProvider); tokenCost != 0 || avatarCost != 0 {
		t.Errorf("custom provider cost = (%v, %v), want none", tokenCost, avatarCost)
	}

	customAvatar := &redisservice.AIAgentSessionTelemetry{
		AvatarMillis:         60000,
		AvatarProvider:       "simli",
		CustomAvatarProvider: "simli",
	}
	if tokenCost, avatarCost := m.EstimateAIAgentLiveCost(customAvatar); tokenCost != 0 || avatarCost != 0 {
		t.Errorf("custom avatar cost = (%v, %v), want none", tokenCost, avatarCost)
	}
}

// Without a session there is nothing to price.
func TestEstimateAIAgentLiveCostWithoutInput(t *testing.T) {
	m := newLiveCostArtifactModel(liveCostInsightsConfig())
	if tokenCost, avatarCost := m.EstimateAIAgentLiveCost(nil); tokenCost != 0 || avatarCost != 0 {
		t.Errorf("nil telemetry cost = (%v, %v), want none", tokenCost, avatarCost)
	}
}
