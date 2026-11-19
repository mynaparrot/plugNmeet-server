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

// newLiveArtifactModel prices a live session per minute and its delegated text
// model and summary per token, the way an OpenAI Live config does.
func newLiveArtifactModel() *ArtifactModel {
	return &ArtifactModel{
		app: &config.AppConfig{Insights: &config.InsightsConfig{
			Providers: map[config.ProviderType][]config.ProviderAccount{
				config.ProviderOpenAI: {{ID: "account-1"}},
			},
			Services: map[insights.ServiceType]*config.ServiceConfig{
				insights.ServiceTypeAIAgent: {
					Provider: config.ProviderOpenAI,
					ID:       "account-1",
					Options: map[string]interface{}{
						"live_model":       "gpt-live-1",
						"delegation_model": "gpt-5.4-mini",
					},
					Pricing: map[string]config.ModelPricing{
						"live_model":       {PricePerMinute: 1.5},
						"delegation_model": {InputPricePerMillionTokens: 0.25, OutputPricePerMillionTokens: 2},
						"summarize_model":  {InputPricePerMillionTokens: 1, OutputPricePerMillionTokens: 3},
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

func TestBuildAIAgentSessionUsagesSeparatedRoles(t *testing.T) {
	log := testLogger()

	t.Run("live session is priced per minute", func(t *testing.T) {
		m := newLiveArtifactModel()
		usages := m.buildAIAgentSessionUsages(&redisservice.AIAgentSessionTelemetry{AudioMillis: 90000}, 0, 0, "", log)
		if len(usages) != 1 {
			t.Fatalf("usages = %d, want only the live entry", len(usages))
		}

		live := usages[0]
		if live.GetTotalTokens() != 0 {
			t.Errorf("a minute-based live session must not report tokens: %+v", live)
		}
		if got := live.GetBreakdown()["audio_millis"]; got != 90000 {
			t.Errorf("audio_millis = %d, want 90000", got)
		}
		// 1.5 minutes at 1.5 per minute
		if !floatEquals(live.GetTotalTokensEstimatedCost(), 2.25) {
			t.Errorf("live cost = %v, want 2.25", live.GetTotalTokensEstimatedCost())
		}
	})

	t.Run("live session falls back to an hourly price", func(t *testing.T) {
		m := newLiveArtifactModel()
		m.app.Insights.Services[insights.ServiceTypeAIAgent].Pricing["live_model"] = config.ModelPricing{PricePerHour: 9}

		usages := m.buildAIAgentSessionUsages(&redisservice.AIAgentSessionTelemetry{AudioMillis: 1800000}, 0, 0, "", log)
		if len(usages) != 1 {
			t.Fatalf("usages = %d, want only the live entry", len(usages))
		}
		// 30 minutes at 9 per hour
		if !floatEquals(usages[0].GetTotalTokensEstimatedCost(), 4.5) {
			t.Errorf("live cost = %v, want 4.5", usages[0].GetTotalTokensEstimatedCost())
		}
	})

	t.Run("delegation tokens are priced apart from the live minutes", func(t *testing.T) {
		m := newLiveArtifactModel()
		telemetry := &redisservice.AIAgentSessionTelemetry{
			AudioMillis:            60000,
			DelegationInputTokens:  1000,
			DelegationOutputTokens: 500,
		}

		usages := m.buildAIAgentSessionUsages(telemetry, 0, 0, "", log)
		if len(usages) != 2 {
			t.Fatalf("usages = %d, want a live entry and a delegation entry", len(usages))
		}

		if !floatEquals(usages[0].GetTotalTokensEstimatedCost(), 1.5) {
			t.Errorf("live cost = %v, want 1.5", usages[0].GetTotalTokensEstimatedCost())
		}

		delegation := usages[1]
		if delegation.GetPromptTokens() != 1000 || delegation.GetCompletionTokens() != 500 || delegation.GetTotalTokens() != 1500 {
			t.Errorf("delegation tokens = (%d, %d, %d), want (1000, 500, 1500)",
				delegation.GetPromptTokens(), delegation.GetCompletionTokens(), delegation.GetTotalTokens())
		}
		if got := delegation.GetBreakdown()["delegation_prompt_tokens"]; got != 1000 {
			t.Errorf("delegation_prompt_tokens = %d, want 1000", got)
		}
		if got := delegation.GetBreakdown()["delegation_completion_tokens"]; got != 500 {
			t.Errorf("delegation_completion_tokens = %d, want 500", got)
		}
		// 1000/1M*0.25 and 500/1M*2 at the delegation model rate
		if !floatEquals(delegation.GetPromptTokensEstimatedCost(), 0.00025) {
			t.Errorf("delegation prompt cost = %v, want 0.00025", delegation.GetPromptTokensEstimatedCost())
		}
		if !floatEquals(delegation.GetCompletionTokensEstimatedCost(), 0.001) {
			t.Errorf("delegation completion cost = %v, want 0.001", delegation.GetCompletionTokensEstimatedCost())
		}
		if !floatEquals(delegation.GetTotalTokensEstimatedCost(), 0.00125) {
			t.Errorf("delegation total cost = %v, want 0.00125", delegation.GetTotalTokensEstimatedCost())
		}
	})

	t.Run("realtime tokens are not priced as live minutes", func(t *testing.T) {
		m := newPricedArtifactModel()
		telemetry := &redisservice.AIAgentSessionTelemetry{
			InputTokens:            1000,
			OutputTokens:           500,
			AudioMillis:            60000,
			DelegationInputTokens:  10,
			DelegationOutputTokens: 5,
		}

		usages := m.buildAIAgentSessionUsages(telemetry, 0, 0, "", log)
		if len(usages) != 2 {
			t.Fatalf("usages = %d, want a realtime entry and a delegation entry", len(usages))
		}
		if !floatEquals(usages[0].GetTotalTokensEstimatedCost(), 0.004) {
			t.Errorf("realtime cost = %v, want 0.004 (tokens only)", usages[0].GetTotalTokensEstimatedCost())
		}
		if got := usages[0].GetBreakdown()["audio_millis"]; got != 60000 {
			t.Errorf("audio_millis = %d, want 60000", got)
		}
	})
}

func TestBuildAIAgentUsageMetadataSeparatedRoles(t *testing.T) {
	log := testLogger()

	t.Run("live minutes, delegation and summary add up", func(t *testing.T) {
		m := newLiveArtifactModel()
		telemetry := &redisservice.AIAgentSessionTelemetry{
			AudioMillis:            120000,
			DelegationInputTokens:  1000,
			DelegationOutputTokens: 500,
		}

		usage := m.buildAIAgentUsageMetadata(telemetry, 200, 100, "", nil, log).GetTokenUsage()
		if usage.GetPromptTokens() != 1200 || usage.GetCompletionTokens() != 600 || usage.GetTotalTokens() != 1800 {
			t.Errorf("tokens = (%d, %d, %d), want (1200, 600, 1800)",
				usage.GetPromptTokens(), usage.GetCompletionTokens(), usage.GetTotalTokens())
		}
		// 1000/1M*0.25 (delegation) + 200/1M*1 (summary)
		if !floatEquals(usage.GetPromptTokensEstimatedCost(), 0.00045) {
			t.Errorf("prompt cost = %v, want 0.00045", usage.GetPromptTokensEstimatedCost())
		}
		// 500/1M*2 (delegation) + 100/1M*3 (summary)
		if !floatEquals(usage.GetCompletionTokensEstimatedCost(), 0.0013) {
			t.Errorf("completion cost = %v, want 0.0013", usage.GetCompletionTokensEstimatedCost())
		}
		// 2 minutes at 1.5 plus the token costs
		if !floatEquals(usage.GetTotalTokensEstimatedCost(), 3.00175) {
			t.Errorf("total cost = %v, want 3.00175", usage.GetTotalTokensEstimatedCost())
		}

		want := map[string]int64{
			"audio_millis":                 120000,
			"delegation_prompt_tokens":     1000,
			"delegation_completion_tokens": 500,
			"summary_prompt_tokens":        200,
			"summary_completion_tokens":    100,
		}
		if len(usage.GetBreakdown()) != len(want) {
			t.Fatalf("breakdown = %v, want %v", usage.GetBreakdown(), want)
		}
		for key, value := range want {
			if got := usage.GetBreakdown()[key]; got != value {
				t.Errorf("%s = %d, want %d", key, got, value)
			}
		}
	})

	t.Run("realtime tokens keep the audio duration out of the cost", func(t *testing.T) {
		m := newPricedArtifactModel()
		telemetry := &redisservice.AIAgentSessionTelemetry{
			InputTokens:  1000,
			OutputTokens: 500,
			AudioMillis:  60000,
		}

		usage := m.buildAIAgentUsageMetadata(telemetry, 0, 0, "", nil, log).GetTokenUsage()
		if !floatEquals(usage.GetPromptTokensEstimatedCost(), 0.002) || !floatEquals(usage.GetCompletionTokensEstimatedCost(), 0.002) {
			t.Errorf("token costs = (%v, %v), want (0.002, 0.002)",
				usage.GetPromptTokensEstimatedCost(), usage.GetCompletionTokensEstimatedCost())
		}
		if !floatEquals(usage.GetTotalTokensEstimatedCost(), 0.004) {
			t.Errorf("total cost = %v, want 0.004", usage.GetTotalTokensEstimatedCost())
		}
	})
}
