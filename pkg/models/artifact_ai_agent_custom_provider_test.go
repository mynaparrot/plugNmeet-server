package models

import (
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"

	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

// aiAgentUsageCostsAreOmitted reports whether a usage entry left every optional
// cost field unset, which is what a BYOK session must do.
func aiAgentUsageCostsAreOmitted(usage *plugnmeet.RoomArtifactTokenUsage) bool {
	return usage != nil && usage.PromptTokensEstimatedCost == nil &&
		usage.CompletionTokensEstimatedCost == nil && usage.TotalTokensEstimatedCost == nil
}

// A session that runs on the host's own provider reports its full usage but
// never a platform cost, and carries the flag for downstream billing.
func TestAIAgentUsageCustomProviderSkipsCost(t *testing.T) {
	log := testLogger()
	m := newLiveArtifactModel()
	telemetry := &redisservice.AIAgentSessionTelemetry{
		InputTokens:            1000,
		OutputTokens:           400,
		AudioInputTokens:       600,
		TextInputTokens:        400,
		AudioOutputTokens:      200,
		DelegationInputTokens:  300,
		DelegationOutputTokens: 100,
		AudioMillis:            60000,
		CustomProvider:         "google",
	}

	t.Run("session usages keep the tokens and drop the cost", func(t *testing.T) {
		usages := m.buildAIAgentSessionUsages(telemetry, 200, 100, "gpt-5.4-mini", log)
		for _, key := range []string{aiAgentUsageKeySession, aiAgentUsageKeyDelegation, aiAgentUsageKeySummary} {
			usage, ok := usages[key]
			if !ok {
				t.Fatalf("usage %q is missing", key)
			}
			// the optional cost fields are omitted instead of reported as 0
			if !aiAgentUsageCostsAreOmitted(usage) {
				t.Errorf("usage %q carries a cost: %+v", key, usage)
			}
		}
		session := usages[aiAgentUsageKeySession]
		if session.GetPromptTokens() != 1000 || session.GetCompletionTokens() != 400 || session.GetTotalTokens() != 1400 {
			t.Errorf("tokens = (%d, %d, %d), want (1000, 400, 1400)",
				session.GetPromptTokens(), session.GetCompletionTokens(), session.GetTotalTokens())
		}
		if got := session.GetBreakdown()["audio_millis"]; got != 60000 {
			t.Errorf("audio_millis = %d, want the session audio", got)
		}
		if got := session.GetBreakdown()["session_audio_prompt_tokens"]; got != 600 {
			t.Errorf("session_audio_prompt_tokens = %d, want the modality split", got)
		}
		if got := usages[aiAgentUsageKeyDelegation].GetTotalTokens(); got != 400 {
			t.Errorf("delegation tokens = %d, want the delegated usage", got)
		}
		if got := usages[aiAgentUsageKeySummary].GetTotalTokens(); got != 300 {
			t.Errorf("summary tokens = %d, want the synthesis usage", got)
		}
	})

	t.Run("the usage metadata keeps the totals and flags the provider", func(t *testing.T) {
		usage := m.buildAIAgentUsageMetadata(telemetry, 200, 100, "gpt-5.4-mini", nil, log).GetTokenUsage()
		if usage.GetPromptTokens() != 1500 || usage.GetCompletionTokens() != 600 || usage.GetTotalTokens() != 2100 {
			t.Errorf("tokens = (%d, %d, %d), want (1500, 600, 2100)",
				usage.GetPromptTokens(), usage.GetCompletionTokens(), usage.GetTotalTokens())
		}
		if !aiAgentUsageCostsAreOmitted(usage) {
			t.Errorf("a custom provider session must carry no cost: %+v", usage)
		}
		if got := usage.GetBreakdown()["custom_provider_google"]; got != 1 {
			t.Errorf("custom_provider_google = %d, want the provider flagged in the breakdown", got)
		}
		if got := usage.GetBreakdown()["delegation_prompt_tokens"]; got != 300 {
			t.Errorf("delegation_prompt_tokens = %d, want the delegated usage", got)
		}
	})

	t.Run("the breakdown key names an openai provider", func(t *testing.T) {
		openai := *telemetry
		openai.CustomProvider = "openai"
		usage := m.buildAIAgentUsageMetadata(&openai, 200, 100, "gpt-5.4-mini", nil, log).GetTokenUsage()
		if got := usage.GetBreakdown()["custom_provider_openai"]; got != 1 {
			t.Errorf("custom_provider_openai = %d, want the provider flagged in the breakdown", got)
		}
		if _, ok := usage.GetBreakdown()["custom_provider_google"]; ok {
			t.Error("an openai session must not be flagged as a google provider")
		}
	})

	t.Run("a platform session keeps its cost", func(t *testing.T) {
		platform := *telemetry
		platform.CustomProvider = ""
		usage := m.buildAIAgentUsageMetadata(&platform, 200, 100, "gpt-5.4-mini", nil, log).GetTokenUsage()
		if usage.GetTotalTokensEstimatedCost() <= 0 {
			t.Errorf("total cost = %v, want a priced session", usage.GetTotalTokensEstimatedCost())
		}
		if usage.TotalTokensEstimatedCost == nil || usage.PromptTokensEstimatedCost == nil || usage.CompletionTokensEstimatedCost == nil {
			t.Errorf("a platform session must always carry the cost fields: %+v", usage)
		}
		for _, key := range []string{"custom_provider", "custom_provider_google", "custom_provider_openai"} {
			if _, ok := usage.GetBreakdown()[key]; ok {
				t.Errorf("a platform session must not carry %q", key)
			}
		}
	})
}
