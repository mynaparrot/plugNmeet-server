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
			t.Fatalf("usages = %d, want one session entry and one summary entry", len(usages))
		}
		if _, ok := usages[aiAgentUsageKeyDelegation]; ok {
			t.Error("no delegation tokens were reported, the delegation entry must be omitted")
		}

		session := usages[aiAgentUsageKeySession]
		if session == nil {
			t.Fatalf("the session entry is missing: %v", usages)
		}
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

		summary := usages[aiAgentUsageKeySummary]
		if summary == nil {
			t.Fatalf("the summary entry is missing: %v", usages)
		}
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
			t.Fatalf("usages = %d, want only the session entry", len(usages))
		}
		session := usages[aiAgentUsageKeySession]
		if session == nil {
			t.Fatalf("the session entry is missing: %v", usages)
		}
		if _, ok := session.GetBreakdown()["audio_millis"]; ok {
			t.Error("audio_millis must be omitted when no audio was reported")
		}
	})

	t.Run("summary usage only", func(t *testing.T) {
		m := newPricedArtifactModel()

		usages := m.buildAIAgentSessionUsages(&redisservice.AIAgentSessionTelemetry{}, 30, 0, "", log)
		if len(usages) != 1 {
			t.Fatalf("usages = %d, want only the summary entry", len(usages))
		}
		summary := usages[aiAgentUsageKeySummary]
		if summary == nil {
			t.Fatalf("the summary entry is missing: %v", usages)
		}
		// the summarize_model pricing entry applies even without a model name
		if !floatEquals(summary.GetTotalTokensEstimatedCost(), 0.00003) {
			t.Errorf("summary cost = %v, want 0.00003", summary.GetTotalTokensEstimatedCost())
		}
		if summary.GetTotalTokens() != 30 {
			t.Errorf("summary total tokens = %d, want 30", summary.GetTotalTokens())
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
		if got := usages[aiAgentUsageKeySummary].GetPromptTokensEstimatedCost(); !floatEquals(got, 0.005) {
			t.Errorf("summary prompt cost = %v, want 0.005", got)
		}

		service.Pricing = map[string]config.ModelPricing{
			"default": {InputPricePerMillionTokens: 9, OutputPricePerMillionTokens: 11},
		}
		usages = m.buildAIAgentSessionUsages(&redisservice.AIAgentSessionTelemetry{}, 1000, 0, "", log)
		if len(usages) != 1 {
			t.Fatalf("usages = %d, want only the summary entry", len(usages))
		}
		// 1000/1M*9 at the "default" price
		if got := usages[aiAgentUsageKeySummary].GetPromptTokensEstimatedCost(); !floatEquals(got, 0.009) {
			t.Errorf("summary prompt cost = %v, want the default price 0.009", got)
		}
	})

	t.Run("usage entries are keyed by role", func(t *testing.T) {
		m := newPricedArtifactModel()
		telemetry := &redisservice.AIAgentSessionTelemetry{
			InputTokens:            10,
			OutputTokens:           5,
			DelegationInputTokens:  10,
			DelegationOutputTokens: 5,
		}

		usages := m.buildAIAgentSessionUsages(telemetry, 10, 5, "", log)
		for _, key := range []string{"session", "delegation", "summary"} {
			if usages[key] == nil {
				t.Errorf("the usage map is missing the %q entry: %v", key, usages)
			}
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

		live := usages[aiAgentUsageKeySession]
		if live == nil {
			t.Fatalf("the live session entry is missing: %v", usages)
		}
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
		if got := usages[aiAgentUsageKeySession].GetTotalTokensEstimatedCost(); !floatEquals(got, 4.5) {
			t.Errorf("live cost = %v, want 4.5", got)
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

		if got := usages[aiAgentUsageKeySession].GetTotalTokensEstimatedCost(); !floatEquals(got, 1.5) {
			t.Errorf("live cost = %v, want 1.5", got)
		}

		delegation := usages[aiAgentUsageKeyDelegation]
		if delegation == nil {
			t.Fatalf("the delegation entry is missing: %v", usages)
		}
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
			t.Fatalf("usages = %d, want a session entry and a delegation entry", len(usages))
		}
		session := usages[aiAgentUsageKeySession]
		if session == nil {
			t.Fatalf("the session entry is missing: %v", usages)
		}
		if !floatEquals(session.GetTotalTokensEstimatedCost(), 0.004) {
			t.Errorf("session cost = %v, want 0.004 (tokens only)", session.GetTotalTokensEstimatedCost())
		}
		if got := session.GetBreakdown()["audio_millis"]; got != 60000 {
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

func TestResolveAIAgentRates(t *testing.T) {
	if _, ok := resolveAIAgentRates(nil); ok {
		t.Error("nil pricing must not report a rate")
	}

	// No token rate at all: minute-priced models report no token cost.
	if _, ok := resolveAIAgentRates(&config.ModelPricing{PricePerMinute: 1.5}); ok {
		t.Error("a pricing block without a token rate must report false")
	}

	// A text-only block: no cached rate, so cached tokens pay the input rate.
	pricing := &config.ModelPricing{InputPricePerMillionTokens: 2, OutputPricePerMillionTokens: 4}
	rates, ok := resolveAIAgentRates(pricing)
	if !ok {
		t.Fatal("a text rate is a usable rate")
	}
	if !floatEquals(rates.textCachedInput, 2) {
		t.Errorf("cached text rate = %v, want the input rate 2: a missing cached rate must not discount", rates.textCachedInput)
	}
	if !floatEquals(rates.audioInput, 2) || !floatEquals(rates.imageInput, 2) || !floatEquals(rates.audioOutput, 4) {
		t.Errorf("a modality without a rate must be billed as text: %+v", rates)
	}
	if !floatEquals(rates.audioCachedInput, 2) || !floatEquals(rates.imageCachedInput, 2) {
		t.Errorf("cached modality tokens must fall back to the text cached rate: %+v", rates)
	}

	// An explicit cached rate is the only way to discount cached tokens.
	explicit := &config.ModelPricing{InputPricePerMillionTokens: 2, CachedInputPricePerMillionTokens: 0.25}
	if rates, _ := resolveAIAgentRates(explicit); !floatEquals(rates.textCachedInput, 0.25) {
		t.Errorf("explicit cached text rate = %v, want 0.25", rates.textCachedInput)
	}

	// A cached rate without an input rate is still a usable rate.
	if _, ok := resolveAIAgentRates(&config.ModelPricing{CachedInputPricePerMillionTokens: 0.5}); !ok {
		t.Error("a cached rate alone is a usable rate")
	}

	// A modality with a rate of its own is discounted only by its own cached
	// rate; without one its cached tokens pay the uncached modality rate.
	multimodal := &config.ModelPricing{
		InputPricePerMillionTokens:       0.60,
		CachedInputPricePerMillionTokens: 0.06,
		AudioInputPricePerMillionTokens:  10,
		OutputPricePerMillionTokens:      2.40,
	}
	rates, _ = resolveAIAgentRates(multimodal)
	if !floatEquals(rates.audioCachedInput, 10) {
		t.Errorf("audio cached rate = %v, want the uncached audio rate 10", rates.audioCachedInput)
	}
	if !floatEquals(rates.imageCachedInput, 0.06) {
		t.Errorf("image cached rate = %v, want the text cached rate 0.06", rates.imageCachedInput)
	}
	// 1000 prompt tokens, 400 of them cached, plus 500 completion tokens.
	prompt, completion := rates.cost(aiAgentModalities{text: 1000, textCached: 400, textOutput: 500})
	if !floatEquals(prompt, 600*0.60/1e6+400*0.06/1e6) {
		t.Errorf("prompt cost = %v, want 600*0.60/1M + 400*0.06/1M", prompt)
	}
	if !floatEquals(completion, 500*2.40/1e6) {
		t.Errorf("completion cost = %v, want 500*2.40/1M", completion)
	}
}

// newMultimodalArtifactModel prices the realtime model of an ai_agent session
// with the given matrix, the way a provider price list does.
func newMultimodalArtifactModel(pricing config.ModelPricing) *ArtifactModel {
	m := newPricedArtifactModel()
	m.app.Insights.Services[insights.ServiceTypeAIAgent].Pricing["realtime_model"] = pricing
	return m
}

func TestBuildAIAgentSessionUsagesModalityPricing(t *testing.T) {
	log := testLogger()

	t.Run("gemini live bills every modality at its own rate", func(t *testing.T) {
		m := newMultimodalArtifactModel(config.ModelPricing{
			InputPricePerMillionTokens:       0.75,
			AudioInputPricePerMillionTokens:  3.00,
			ImageInputPricePerMillionTokens:  1.00,
			OutputPricePerMillionTokens:      4.50,
			AudioOutputPricePerMillionTokens: 12.00,
		})
		telemetry := &redisservice.AIAgentSessionTelemetry{
			InputTokens:          1000000,
			CachedInputTokens:    100000,
			TextInputTokens:      400000,
			AudioInputTokens:     550000,
			ImageInputTokens:     50000,
			OutputTokens:         200000,
			TextOutputTokens:     150000,
			AudioOutputTokens:    50000,
			ThinkingOutputTokens: 30000,
		}

		session := m.buildAIAgentSessionUsages(telemetry, 0, 0, "", log)[aiAgentUsageKeySession]
		if session == nil {
			t.Fatal("the session entry is missing")
		}

		// Text 0.4M uncached plus 0.1M cached, audio 0.55M, image 0.05M. No
		// cached rate is configured, so the cached text tokens pay 0.75 too.
		wantPrompt := 0.3*0.75 + 0.1*0.75 + 0.55*3.00 + 0.05*1.00
		if !floatEquals(session.GetPromptTokensEstimatedCost(), wantPrompt) {
			t.Errorf("session prompt cost = %v, want %v", session.GetPromptTokensEstimatedCost(), wantPrompt)
		}
		// Text 0.15M at the output rate, audio 0.05M at the audio output rate.
		wantCompletion := 0.15*4.50 + 0.05*12.00
		if !floatEquals(session.GetCompletionTokensEstimatedCost(), wantCompletion) {
			t.Errorf("session completion cost = %v, want %v", session.GetCompletionTokensEstimatedCost(), wantCompletion)
		}

		want := map[string]int64{
			"session_prompt_tokens":              1000000,
			"session_completion_tokens":          200000,
			"session_cached_prompt_tokens":       100000,
			"session_text_prompt_tokens":         400000,
			"session_audio_prompt_tokens":        550000,
			"session_image_prompt_tokens":        50000,
			"session_cached_text_prompt_tokens":  100000,
			"session_text_completion_tokens":     150000,
			"session_audio_completion_tokens":    50000,
			"session_thinking_completion_tokens": 30000,
		}
		if len(session.GetBreakdown()) != len(want) {
			t.Fatalf("breakdown = %v, want %v", session.GetBreakdown(), want)
		}
		for key, value := range want {
			if got := session.GetBreakdown()[key]; got != value {
				t.Errorf("%s = %d, want %d", key, got, value)
			}
		}

		usage := m.buildAIAgentUsageMetadata(telemetry, 0, 0, "", nil, log).GetTokenUsage()
		if !floatEquals(usage.GetPromptTokensEstimatedCost(), wantPrompt) ||
			!floatEquals(usage.GetCompletionTokensEstimatedCost(), wantCompletion) {
			t.Errorf("metadata costs = (%v, %v), want (%v, %v)",
				usage.GetPromptTokensEstimatedCost(), usage.GetCompletionTokensEstimatedCost(), wantPrompt, wantCompletion)
		}
		if got := usage.GetBreakdown()["session_audio_prompt_tokens"]; got != 550000 {
			t.Errorf("metadata session_audio_prompt_tokens = %d, want 550000", got)
		}
	})

	t.Run("openai realtime bills the cached subset of every modality", func(t *testing.T) {
		m := newMultimodalArtifactModel(config.ModelPricing{
			InputPricePerMillionTokens:            0.60,
			CachedInputPricePerMillionTokens:      0.06,
			AudioInputPricePerMillionTokens:       10.00,
			AudioCachedInputPricePerMillionTokens: 0.30,
			ImageInputPricePerMillionTokens:       0.80,
			ImageCachedInputPricePerMillionTokens: 0.08,
			OutputPricePerMillionTokens:           2.40,
			AudioOutputPricePerMillionTokens:      20.00,
		})
		telemetry := &redisservice.AIAgentSessionTelemetry{
			InputTokens:            1000000,
			CachedInputTokens:      300000,
			TextInputTokens:        500000,
			AudioInputTokens:       450000,
			ImageInputTokens:       50000,
			CachedTextInputTokens:  200000,
			CachedAudioInputTokens: 90000,
			CachedImageInputTokens: 10000,
			OutputTokens:           200000,
			TextOutputTokens:       20000,
			AudioOutputTokens:      180000,
		}

		session := m.buildAIAgentSessionUsages(telemetry, 0, 0, "", log)[aiAgentUsageKeySession]
		if session == nil {
			t.Fatal("the session entry is missing")
		}

		wantPrompt := 0.3*0.60 + 0.2*0.06 + 0.36*10.00 + 0.09*0.30 + 0.04*0.80 + 0.01*0.08
		if !floatEquals(session.GetPromptTokensEstimatedCost(), wantPrompt) {
			t.Errorf("session prompt cost = %v, want %v", session.GetPromptTokensEstimatedCost(), wantPrompt)
		}
		wantCompletion := 0.02*2.40 + 0.18*20.00
		if !floatEquals(session.GetCompletionTokensEstimatedCost(), wantCompletion) {
			t.Errorf("session completion cost = %v, want %v", session.GetCompletionTokensEstimatedCost(), wantCompletion)
		}

		want := map[string]int64{
			"session_cached_text_prompt_tokens":  200000,
			"session_cached_audio_prompt_tokens": 90000,
			"session_cached_image_prompt_tokens": 10000,
			"session_text_prompt_tokens":         500000,
			"session_audio_prompt_tokens":        450000,
			"session_image_prompt_tokens":        50000,
			"session_text_completion_tokens":     20000,
			"session_audio_completion_tokens":    180000,
		}
		for key, value := range want {
			if got := session.GetBreakdown()[key]; got != value {
				t.Errorf("%s = %d, want %d", key, got, value)
			}
		}
		if _, ok := session.GetBreakdown()["session_thinking_completion_tokens"]; ok {
			t.Error("openai reports no thinking tokens, the key must be omitted")
		}
	})

	t.Run("an audio-only turn is not priced as text", func(t *testing.T) {
		m := newMultimodalArtifactModel(config.ModelPricing{
			InputPricePerMillionTokens:       0.75,
			AudioInputPricePerMillionTokens:  3.00,
			OutputPricePerMillionTokens:      4.50,
			AudioOutputPricePerMillionTokens: 12.00,
		})
		// The provider reported the split of the response, not of the prompt.
		telemetry := &redisservice.AIAgentSessionTelemetry{
			InputTokens:       1000000,
			AudioInputTokens:  600000,
			OutputTokens:      100000,
			AudioOutputTokens: 100000,
		}

		session := m.buildAIAgentSessionUsages(telemetry, 0, 0, "", log)[aiAgentUsageKeySession]
		if got := session.GetBreakdown()["session_audio_prompt_tokens"]; got != 600000 {
			t.Errorf("session_audio_prompt_tokens = %d, want 600000", got)
		}
		// The unattributed remainder is text, so no token goes unbilled.
		if got := session.GetBreakdown()["session_text_prompt_tokens"]; got != 400000 {
			t.Errorf("session_text_prompt_tokens = %d, want 400000", got)
		}
		wantPrompt := 0.4*0.75 + 0.6*3.00
		if !floatEquals(session.GetPromptTokensEstimatedCost(), wantPrompt) {
			t.Errorf("session prompt cost = %v, want %v", session.GetPromptTokensEstimatedCost(), wantPrompt)
		}
		if !floatEquals(session.GetCompletionTokensEstimatedCost(), 0.1*12.00) {
			t.Errorf("session completion cost = %v, want %v", session.GetCompletionTokensEstimatedCost(), 0.1*12.00)
		}
	})
}

func TestBuildAIAgentUsageCachedPromptTokens(t *testing.T) {
	log := testLogger()

	t.Run("cached prompt tokens are reported but not discounted by default", func(t *testing.T) {
		m := newPricedArtifactModel()
		telemetry := &redisservice.AIAgentSessionTelemetry{InputTokens: 1000, CachedInputTokens: 400, OutputTokens: 500}

		session := m.buildAIAgentSessionUsages(telemetry, 0, 0, "", log)[aiAgentUsageKeySession]
		if session == nil {
			t.Fatal("the session entry is missing")
		}
		if got := session.GetBreakdown()["session_cached_prompt_tokens"]; got != 400 {
			t.Errorf("session_cached_prompt_tokens = %d, want 400", got)
		}
		// No cached rate is configured, so all 1000 prompt tokens pay 2 per 1M.
		if !floatEquals(session.GetPromptTokensEstimatedCost(), 0.002) {
			t.Errorf("session prompt cost = %v, want 0.002", session.GetPromptTokensEstimatedCost())
		}
		if !floatEquals(session.GetTotalTokensEstimatedCost(), 0.004) {
			t.Errorf("session total cost = %v, want 0.004", session.GetTotalTokensEstimatedCost())
		}
		if session.GetPromptTokens() != 1000 || session.GetTotalTokens() != 1500 {
			t.Errorf("cached tokens must stay part of the prompt tokens: %+v", session)
		}

		usage := m.buildAIAgentUsageMetadata(telemetry, 0, 0, "", nil, log).GetTokenUsage()
		if got := usage.GetBreakdown()["session_cached_prompt_tokens"]; got != 400 {
			t.Errorf("metadata session_cached_prompt_tokens = %d, want 400", got)
		}
		if !floatEquals(usage.GetPromptTokensEstimatedCost(), 0.002) {
			t.Errorf("metadata prompt cost = %v, want 0.002", usage.GetPromptTokensEstimatedCost())
		}
		if !floatEquals(usage.GetTotalTokensEstimatedCost(), 0.004) {
			t.Errorf("metadata total cost = %v, want 0.004", usage.GetTotalTokensEstimatedCost())
		}
	})

	t.Run("an explicit cached rate discounts the cached tokens", func(t *testing.T) {
		m := newPricedArtifactModel()
		m.app.Insights.Services[insights.ServiceTypeAIAgent].Pricing["realtime_model"] = config.ModelPricing{
			InputPricePerMillionTokens:       2,
			OutputPricePerMillionTokens:      4,
			CachedInputPricePerMillionTokens: 0.25,
		}

		telemetry := &redisservice.AIAgentSessionTelemetry{InputTokens: 1000, CachedInputTokens: 1000}
		session := m.buildAIAgentSessionUsages(telemetry, 0, 0, "", log)[aiAgentUsageKeySession]
		if !floatEquals(session.GetPromptTokensEstimatedCost(), 0.00025) {
			t.Errorf("session prompt cost = %v, want 0.00025", session.GetPromptTokensEstimatedCost())
		}
	})

	t.Run("cached tokens never exceed the input tokens", func(t *testing.T) {
		m := newPricedArtifactModel()
		telemetry := &redisservice.AIAgentSessionTelemetry{InputTokens: 100, CachedInputTokens: 400}

		session := m.buildAIAgentSessionUsages(telemetry, 0, 0, "", log)[aiAgentUsageKeySession]
		if got := session.GetBreakdown()["session_cached_prompt_tokens"]; got != 100 {
			t.Errorf("session_cached_prompt_tokens = %d, want it clamped to 100", got)
		}
		// everything cached, at the input rate: 100/1M*2
		if !floatEquals(session.GetPromptTokensEstimatedCost(), 0.0002) {
			t.Errorf("session prompt cost = %v, want 0.0002", session.GetPromptTokensEstimatedCost())
		}
	})

	t.Run("no cached tokens keeps the breakdown without the key", func(t *testing.T) {
		m := newPricedArtifactModel()
		telemetry := &redisservice.AIAgentSessionTelemetry{InputTokens: 1000, OutputTokens: 500}

		session := m.buildAIAgentSessionUsages(telemetry, 0, 0, "", log)[aiAgentUsageKeySession]
		if _, ok := session.GetBreakdown()["session_cached_prompt_tokens"]; ok {
			t.Error("session_cached_prompt_tokens must be omitted when nothing was cached")
		}
		if !floatEquals(session.GetPromptTokensEstimatedCost(), 0.002) {
			t.Errorf("session prompt cost = %v, want 0.002", session.GetPromptTokensEstimatedCost())
		}
	})
}
