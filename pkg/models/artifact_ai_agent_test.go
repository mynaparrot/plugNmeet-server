package models

import (
	"strings"
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
)

// newAvatarArtifactModel prices a live session and its realtime avatar per
// minute, the way a config with an avatar does.
func newAvatarArtifactModel() *ArtifactModel {
	return &ArtifactModel{
		app: &config.AppConfig{Insights: &config.InsightsConfig{
			Providers: map[config.ProviderType][]config.ProviderAccount{
				config.ProviderOpenAI: {{ID: "account-1"}},
			},
			Services: map[insights.ServiceType]*config.ServiceConfig{
				insights.ServiceTypeAIAgent: {
					Provider: config.ProviderOpenAI,
					ID:       "account-1",
					Options:  map[string]interface{}{"live_model": "gpt-live-1"},
					Pricing: map[string]config.ModelPricing{
						"live_model":   {PricePerMinute: 1.5},
						"avatar_model": {PricePerMinute: 0.6},
					},
				},
			},
		}},
	}
}

// avatarTelemetry returns a session that burned one minute of speech and two
// minutes of avatar video.
func avatarTelemetry() *redisservice.AIAgentSessionTelemetry {
	return &redisservice.AIAgentSessionTelemetry{
		AudioMillis:    60000,
		AvatarMillis:   120000,
		AvatarProvider: "beyond_presence",
	}
}

// hasAvatarUsageKey reports whether a token breakdown carries avatar data, which
// would mean the token usage is no longer pure LLM usage.
func hasAvatarUsageKey(breakdown map[string]int64) bool {
	for key := range breakdown {
		if strings.Contains(key, "avatar") {
			return true
		}
	}
	return false
}

// hasCustomAvatarFlag reports whether the breakdown carries a custom avatar flag.
func hasCustomAvatarFlag(breakdown map[string]int64) bool {
	for key := range breakdown {
		if strings.HasPrefix(key, aiAgentUsageKeyCustomAvatarProviderPrefix) {
			return true
		}
	}
	return false
}

// The token usage of the session stays strictly token/speech-LLM: the realtime
// avatar is a per-minute duration and never a role of the token map.
func TestBuildAIAgentSessionUsagesExcludesAvatar(t *testing.T) {
	log := testLogger()
	m := newAvatarArtifactModel()

	usages := m.buildAIAgentSessionUsages(avatarTelemetry(), 0, 0, "", log)

	if len(usages) != 1 {
		t.Fatalf("usages = %v, want only the speech session role", usages)
	}
	if _, ok := usages["avatar"]; ok {
		t.Errorf("the avatar must not be a token usage role: %v", usages)
	}
	// the speech model keeps its own per-minute entry, with no avatar cost mixed in
	session := usages[aiAgentUsageKeySession]
	if session == nil || !floatEquals(session.GetTotalTokensEstimatedCost(), 1.5) {
		t.Fatalf("session usage = %+v, want the speech cost of 1.5", session)
	}
	if hasAvatarUsageKey(session.GetBreakdown()) {
		t.Errorf("the session breakdown carries avatar data: %v", session.GetBreakdown())
	}
}

// The AI_AGENT_USAGE token usage never folds the avatar cost into its total or
// its breakdown.
func TestBuildAIAgentUsageMetadataExcludesAvatar(t *testing.T) {
	log := testLogger()
	m := newAvatarArtifactModel()

	usage := m.buildAIAgentUsageMetadata(avatarTelemetry(), 0, 0, "", nil, log).GetTokenUsage()
	if hasAvatarUsageKey(usage.GetBreakdown()) {
		t.Errorf("the token usage breakdown carries avatar data: %v", usage.GetBreakdown())
	}
	// only the minute of speech at 1.5, the two avatar minutes are not priced here
	if !floatEquals(usage.GetTotalTokensEstimatedCost(), 1.5) {
		t.Errorf("total cost = %v, want the speech cost of 1.5 alone", usage.GetTotalTokensEstimatedCost())
	}

	// A host-provided LLM leaves no platform token cost at all, so the avatar
	// minutes must not keep the total alive.
	customProvider := avatarTelemetry()
	customProvider.CustomProvider = "google"
	usage = m.buildAIAgentUsageMetadata(customProvider, 0, 0, "", nil, log).GetTokenUsage()
	if usage.TotalTokensEstimatedCost != nil {
		t.Errorf("total cost = %v, want it omitted for a host-provided LLM", usage.GetTotalTokensEstimatedCost())
	}
	if got := usage.GetBreakdown()[aiAgentUsageKeyCustomProviderPrefix+"google"]; got != 1 {
		t.Errorf("the custom provider flag is missing: %v", usage.GetBreakdown())
	}
}

// The avatar is recorded as a duration usage of its own, priced per minute of
// generated video.
func TestBuildAIAgentAvatarDurationUsage(t *testing.T) {
	log := testLogger()
	m := newAvatarArtifactModel()

	if usage := m.buildAIAgentAvatarDurationUsage(nil, log); usage != nil {
		t.Errorf("usage = %+v, want nil without telemetry", usage)
	}
	if usage := m.buildAIAgentAvatarDurationUsage(&redisservice.AIAgentSessionTelemetry{AudioMillis: 60000}, log); usage != nil {
		t.Errorf("usage = %+v, want nil without avatar minutes", usage)
	}

	usage := m.buildAIAgentAvatarDurationUsage(avatarTelemetry(), log)
	if usage == nil {
		t.Fatal("the avatar duration usage is missing")
	}
	if got := usage.GetDurationSec(); got != 120 {
		t.Errorf("duration_sec = %d, want 120", got)
	}
	if got := usage.GetBreakdown()["avatar_millis"]; got != 120000 {
		t.Errorf("avatar_millis = %d, want the exact duration 120000", got)
	}
	if hasCustomAvatarFlag(usage.GetBreakdown()) {
		t.Errorf("a platform avatar must not be flagged as custom: %v", usage.GetBreakdown())
	}
	if usage.DurationSecEstimatedCost == nil {
		t.Fatal("the avatar cost is missing")
	}
	// 120000ms / 60000 * 0.6
	if !floatEquals(usage.GetDurationSecEstimatedCost(), 1.2) {
		t.Errorf("avatar cost = %v, want 1.2", usage.GetDurationSecEstimatedCost())
	}

	// anything shorter than a second is still a started second
	short := m.buildAIAgentAvatarDurationUsage(&redisservice.AIAgentSessionTelemetry{AvatarMillis: 400}, log)
	if short == nil || short.GetDurationSec() != 1 {
		t.Fatalf("short usage = %+v, want a duration of 1 second", short)
	}
	if got := short.GetBreakdown()["avatar_millis"]; got != 400 {
		t.Errorf("avatar_millis = %d, want the exact duration 400", got)
	}

	// A provider priced apart from the generic avatar role wins over avatar_model.
	t.Run("a provider-specific price overrides the avatar role price", func(t *testing.T) {
		priced := newAvatarArtifactModel()
		priced.app.Insights.Services[insights.ServiceTypeAIAgent].Pricing["simli"] = config.ModelPricing{PricePerMinute: 2}

		telemetry := avatarTelemetry()
		telemetry.AvatarProvider = " Simli "

		usage := priced.buildAIAgentAvatarDurationUsage(telemetry, log)
		if usage == nil {
			t.Fatal("the avatar duration usage is missing")
		}
		// 120000ms / 60000 * 2, the simli rate rather than avatar_model's 0.6
		if got := usage.GetDurationSecEstimatedCost(); !floatEquals(got, 4) {
			t.Errorf("avatar cost = %v, want the simli rate of 4", got)
		}
	})
}

// A room that brought its own avatar key records the duration but is never
// priced for it.
func TestBuildAIAgentAvatarDurationUsageCustomAvatarSkipsCost(t *testing.T) {
	log := testLogger()
	m := newAvatarArtifactModel()

	telemetry := avatarTelemetry()
	telemetry.CustomAvatarProvider = "Beyond_Presence"

	usage := m.buildAIAgentAvatarDurationUsage(telemetry, log)
	if usage == nil {
		t.Fatal("the avatar duration usage is missing")
	}
	if usage.DurationSecEstimatedCost != nil {
		t.Errorf("avatar cost = %v, want it omitted for a custom avatar", usage.GetDurationSecEstimatedCost())
	}
	if got := usage.GetDurationSec(); got != 120 {
		t.Errorf("duration_sec = %d, want the duration kept", got)
	}
	if got := usage.GetBreakdown()[aiAgentUsageKeyCustomAvatarProviderPrefix+"beyond_presence"]; got != 1 {
		t.Errorf("the custom avatar flag is missing: %v", usage.GetBreakdown())
	}
}

// The metadata wrapper is the AI_AGENT_AVATAR_USAGE artifact's metadata, and is
// absent when the session had no avatar.
func TestBuildAIAgentAvatarUsageMetadata(t *testing.T) {
	log := testLogger()
	m := newAvatarArtifactModel()

	if metadata := m.buildAIAgentAvatarUsageMetadata(nil, nil, log); metadata != nil {
		t.Errorf("metadata = %+v, want nil without telemetry", metadata)
	}
	if metadata := m.buildAIAgentAvatarUsageMetadata(&redisservice.AIAgentSessionTelemetry{AudioMillis: 60000}, nil, log); metadata != nil {
		t.Errorf("metadata = %+v, want nil without avatar minutes", metadata)
	}

	fileArtifactId := "file-artifact-1"
	metadata := m.buildAIAgentAvatarUsageMetadata(avatarTelemetry(), &fileArtifactId, log)
	if metadata == nil {
		t.Fatal("the avatar usage metadata is missing")
	}
	if metadata.GetTokenUsage() != nil {
		t.Errorf("metadata = %+v, want a duration usage only", metadata)
	}
	duration := metadata.GetDurationUsage()
	if duration == nil || duration.GetDurationSec() != 120 || !floatEquals(duration.GetDurationSecEstimatedCost(), 1.2) {
		t.Fatalf("duration usage = %+v, want 120 seconds at 1.2", duration)
	}
	if got := metadata.GetReferenceArtifactId(); got != fileArtifactId {
		t.Errorf("reference artifact id = %q, want %q", got, fileArtifactId)
	}
}

// avatarSessionSummary returns a summary of a session that used an avatar, the
// way CreateAIAgentSessionSummaryArtifact splits the usage.
func avatarSessionSummary(m *ArtifactModel, log *logrus.Entry) *plugnmeet.AIAgentSessionSummary {
	telemetry := avatarTelemetry()
	return &plugnmeet.AIAgentSessionSummary{
		SummarySource: "llm",
		UserInfo:      &plugnmeet.RoomArtifactUserInfo{UserId: "learner-1"},
		Usage:         m.buildAIAgentSessionUsages(telemetry, 0, 0, "", log),
		AvatarUsage:   m.buildAIAgentAvatarDurationUsage(telemetry, log),
	}
}

// The summary JSON carries avatar_usage next to the token usage, and omits it
// entirely when the session had no avatar.
func TestMarshalAIAgentSessionSummaryAvatarUsage(t *testing.T) {
	log := testLogger()
	m := newAvatarArtifactModel()

	content, err := marshalAIAgentSessionSummary(avatarSessionSummary(m, log))
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	raw := string(content)
	for _, want := range []string{
		`"avatar_usage"`,
		`"duration_sec"`,
		`"avatar_millis"`,
		`"duration_sec_estimated_cost"`,
		`"usage"`,
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("summary JSON does not contain %s:\n%s", want, raw)
		}
	}
	// the avatar duration sits beside the token usage, never inside it
	if usageStart, avatarStart := strings.Index(raw, `"usage"`), strings.Index(raw, `"avatar_usage"`); usageStart < 0 || avatarStart < 0 || avatarStart < usageStart {
		t.Errorf("avatar_usage must follow the token usage:\n%s", raw)
	}
	// the avatar never leaks into the token usage of the summary
	if strings.Contains(raw, `"avatar": {`) {
		t.Errorf("summary JSON carries an avatar token role:\n%s", raw)
	}

	// The written avatar usage round trips into the protocol type.
	decoded := new(plugnmeet.AIAgentSessionSummary)
	if err = protojson.Unmarshal(content, decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if got := decoded.GetAvatarUsage().GetDurationSec(); got != 120 {
		t.Errorf("decoded duration_sec = %d, want 120", got)
	}
	if got := decoded.GetAvatarUsage().GetBreakdown()["avatar_millis"]; got != 120000 {
		t.Errorf("decoded avatar_millis = %d, want the exact duration 120000", got)
	}
	if got := decoded.GetAvatarUsage().GetDurationSecEstimatedCost(); !floatEquals(got, 1.2) {
		t.Errorf("decoded avatar cost = %v, want 1.2", got)
	}
	if got := decoded.GetUsage()[aiAgentUsageKeySession].GetTotalTokensEstimatedCost(); !floatEquals(got, 1.5) {
		t.Errorf("decoded session cost = %v, want the speech cost of 1.5", got)
	}

	// a session without an avatar omits the field instead of serializing a zero
	withoutAvatar := avatarSessionSummary(m, log)
	withoutAvatar.AvatarUsage = nil
	anonymous, err := marshalAIAgentSessionSummary(withoutAvatar)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if body := string(anonymous); strings.Contains(body, `"avatar_usage"`) {
		t.Errorf("summary JSON must omit avatar_usage without an avatar:\n%s", body)
	}
}
