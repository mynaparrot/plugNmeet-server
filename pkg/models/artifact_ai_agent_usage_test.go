package models

import (
	"math"
	"testing"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

// newPricedArtifactModel builds an ArtifactModel whose ai_agent service prices
// the realtime and the summarize role, the way config_sample.yaml does.
func newPricedArtifactModel() *ArtifactModel {
	return &ArtifactModel{
		app: &config.AppConfig{Insights: &config.InsightsConfig{
			Providers: map[config.ProviderType][]config.ProviderAccount{
				config.ProviderOpenAI: {{ID: "account-1"}},
			},
			Services: map[insights.ServiceType]*config.ServiceConfig{
				insights.ServiceTypeAIAgent: {
					Provider: config.ProviderOpenAI,
					ID:       "account-1",
					Options:  map[string]interface{}{"realtime_model": "realtime-model"},
					Pricing: map[string]config.ModelPricing{
						"realtime_model":  {InputPricePerMillionTokens: 2, OutputPricePerMillionTokens: 4},
						"summarize_model": {InputPricePerMillionTokens: 1, OutputPricePerMillionTokens: 3},
					},
				},
			},
		}},
	}
}

func testLogger() *logrus.Entry {
	logger := logrus.New()
	logger.SetLevel(logrus.PanicLevel)
	return logger.WithField("test", "artifact_ai_agent_usage")
}

func floatEquals(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func TestBuildAIAgentSessionUsages(t *testing.T) {
	log := testLogger()

	t.Run("session and summary usage stay separate", func(t *testing.T) {
		m := newPricedArtifactModel()
		telemetry := &redisservice.AIAgentSessionTelemetry{InputTokens: 1000, OutputTokens: 500, AudioMillis: 65000}

		usages := m.buildAIAgentSessionUsages(telemetry, 200, 100, "summary-model", log)
		if len(usages) != 2 {
			t.Fatalf("usages = %d, want one realtime entry and one summary entry", len(usages))
		}

		session := usages[0]
		if session.GetPromptTokens() != 1000 || session.GetCompletionTokens() != 500 || session.GetTotalTokens() != 1500 {
			t.Errorf("session tokens = (%d, %d, %d), want (1000, 500, 1500)",
				session.GetPromptTokens(), session.GetCompletionTokens(), session.GetTotalTokens())
		}
		if got := session.GetBreakdown()["session_prompt_tokens"]; got != 1000 {
			t.Errorf("session_prompt_tokens = %d, want 1000", got)
		}
		if got := session.GetBreakdown()["session_completion_tokens"]; got != 500 {
			t.Errorf("session_completion_tokens = %d, want 500", got)
		}
		if got := session.GetBreakdown()["audio_millis"]; got != 65000 {
			t.Errorf("audio_millis = %d, want 65000", got)
		}
		// 1000/1M*2 and 500/1M*4 at the realtime model rate
		if !floatEquals(session.GetPromptTokensEstimatedCost(), 0.002) {
			t.Errorf("session prompt cost = %v, want 0.002", session.GetPromptTokensEstimatedCost())
		}
		if !floatEquals(session.GetCompletionTokensEstimatedCost(), 0.002) {
			t.Errorf("session completion cost = %v, want 0.002", session.GetCompletionTokensEstimatedCost())
		}
		if !floatEquals(session.GetTotalTokensEstimatedCost(), 0.004) {
			t.Errorf("session total cost = %v, want 0.004", session.GetTotalTokensEstimatedCost())
		}

		summary := usages[1]
		if summary.GetPromptTokens() != 200 || summary.GetCompletionTokens() != 100 || summary.GetTotalTokens() != 300 {
			t.Errorf("summary tokens = (%d, %d, %d), want (200, 100, 300)",
				summary.GetPromptTokens(), summary.GetCompletionTokens(), summary.GetTotalTokens())
		}
		if got := summary.GetBreakdown()["summary_prompt_tokens"]; got != 200 {
			t.Errorf("summary_prompt_tokens = %d, want 200", got)
		}
		if got := summary.GetBreakdown()["summary_completion_tokens"]; got != 100 {
			t.Errorf("summary_completion_tokens = %d, want 100", got)
		}
		if _, ok := summary.GetBreakdown()["audio_millis"]; ok {
			t.Error("the summary entry must not report the realtime audio duration")
		}
		// 200/1M*1 and 100/1M*3 at the summarize model rate
		if !floatEquals(summary.GetPromptTokensEstimatedCost(), 0.0002) {
			t.Errorf("summary prompt cost = %v, want 0.0002", summary.GetPromptTokensEstimatedCost())
		}
		if !floatEquals(summary.GetCompletionTokensEstimatedCost(), 0.0003) {
			t.Errorf("summary completion cost = %v, want 0.0003", summary.GetCompletionTokensEstimatedCost())
		}
		if !floatEquals(summary.GetTotalTokensEstimatedCost(), 0.0005) {
			t.Errorf("summary total cost = %v, want 0.0005", summary.GetTotalTokensEstimatedCost())
		}
	})

	t.Run("session usage only", func(t *testing.T) {
		m := newPricedArtifactModel()
		telemetry := &redisservice.AIAgentSessionTelemetry{InputTokens: 10, OutputTokens: 5}

		usages := m.buildAIAgentSessionUsages(telemetry, 0, 0, "", log)
		if len(usages) != 1 {
			t.Fatalf("usages = %d, want only the realtime entry", len(usages))
		}
		if _, ok := usages[0].GetBreakdown()["audio_millis"]; ok {
			t.Error("audio_millis must be omitted when no audio was reported")
		}
	})

	t.Run("summary usage only", func(t *testing.T) {
		m := newPricedArtifactModel()

		usages := m.buildAIAgentSessionUsages(&redisservice.AIAgentSessionTelemetry{}, 30, 0, "", log)
		if len(usages) != 1 {
			t.Fatalf("usages = %d, want only the summary entry", len(usages))
		}
		// the summarize_model pricing entry applies even without a model name
		if !floatEquals(usages[0].GetTotalTokensEstimatedCost(), 0.00003) {
			t.Errorf("summary cost = %v, want 0.00003", usages[0].GetTotalTokensEstimatedCost())
		}
		if usages[0].GetTotalTokens() != 30 {
			t.Errorf("summary total tokens = %d, want 30", usages[0].GetTotalTokens())
		}
	})

	t.Run("pricing falls back to the model name and then to default", func(t *testing.T) {
		m := newPricedArtifactModel()
		service := m.app.Insights.Services[insights.ServiceTypeAIAgent]

		service.Pricing = map[string]config.ModelPricing{
			"summary-model": {InputPricePerMillionTokens: 5, OutputPricePerMillionTokens: 7},
		}
		usages := m.buildAIAgentSessionUsages(&redisservice.AIAgentSessionTelemetry{}, 1000, 0, "summary-model", log)
		if len(usages) != 1 {
			t.Fatalf("usages = %d, want only the summary entry", len(usages))
		}
		// 1000/1M*5 at the price of the concrete model name
		if !floatEquals(usages[0].GetPromptTokensEstimatedCost(), 0.005) {
			t.Errorf("summary prompt cost = %v, want 0.005", usages[0].GetPromptTokensEstimatedCost())
		}

		service.Pricing = map[string]config.ModelPricing{
			"default": {InputPricePerMillionTokens: 9, OutputPricePerMillionTokens: 11},
		}
		usages = m.buildAIAgentSessionUsages(&redisservice.AIAgentSessionTelemetry{}, 1000, 0, "", log)
		if len(usages) != 1 {
			t.Fatalf("usages = %d, want only the summary entry", len(usages))
		}
		// 1000/1M*9 at the "default" price
		if !floatEquals(usages[0].GetPromptTokensEstimatedCost(), 0.009) {
			t.Errorf("summary prompt cost = %v, want the default price 0.009", usages[0].GetPromptTokensEstimatedCost())
		}
	})

	t.Run("no usage at all", func(t *testing.T) {
		m := newPricedArtifactModel()
		for _, telemetry := range []*redisservice.AIAgentSessionTelemetry{nil, {}} {
			if usages := m.buildAIAgentSessionUsages(telemetry, 0, 0, "", log); len(usages) != 0 {
				t.Fatalf("usages = %d, want none", len(usages))
			}
		}
	})
}
