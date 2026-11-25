package models

import (
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

// CreateAIAgentSessionSummaryArtifact writes the summary JSON as a downloadable
// file artifact, then records the session's token usage next to it.
func (m *ArtifactModel) CreateAIAgentSessionSummaryArtifact(roomTableId uint64, roomId, roomSid string, summary *plugnmeet.AIAgentSessionSummary, telemetry *redisservice.AIAgentSessionTelemetry, summaryPromptTokens, summaryCompletionTokens uint32, summarizeModel string, log *logrus.Entry) (*dbmodels.RoomArtifact, error) {
	log = log.WithField("method", "CreateAIAgentSessionSummaryArtifact")
	if summary == nil {
		return nil, fmt.Errorf("ai agent session summary is required")
	}

	roomInfo, err := m.ds.GetRoomInfoByTableId(roomTableId)
	if err != nil {
		return nil, fmt.Errorf("failed to get room info for room %d: %w", roomTableId, err)
	}
	if roomInfo == nil {
		return nil, fmt.Errorf("room not found for room %d", roomTableId)
	}

	// Breakout session artifacts are owned by the parent room, exactly like the
	// meeting summary usage artifact. Resolve the owner before building the file
	// path so both the summary and its usage live under the parent room.
	ownerRoomId, ownerRoomSid, ownerTableId := roomId, roomSid, roomTableId
	isBreakout := roomInfo.IsBreakoutRoom != 0
	if isBreakout {
		parentRoomId, parentTableId, perr := helpers.ParseBreakoutParentRoomRef(roomInfo.ParentRoomID)
		if perr != nil {
			log.WithError(perr).Error("failed to parse the parent room reference, keeping the ai agent artifacts in the breakout room")
			isBreakout = false
		} else if parentInfo, perr := m.ds.GetRoomInfoByTableId(parentTableId); perr != nil || parentInfo == nil || parentInfo.RoomId != parentRoomId {
			log.WithError(perr).Error("failed to resolve the parent room, keeping the ai agent artifacts in the breakout room")
			isBreakout = false
		} else {
			ownerRoomId, ownerRoomSid, ownerTableId = parentInfo.RoomId, parentInfo.Sid, parentInfo.ID
		}
	}

	// the written JSON and the returned summary both carry the separated usage
	// of this learner's session
	summary.Usage = m.buildAIAgentSessionUsages(telemetry, summaryPromptTokens, summaryCompletionTokens, summarizeModel, log)
	// The realtime avatar is a duration, not a token role, so it is kept apart
	// from the token usage of the summary.
	summary.AvatarUsage = m.buildAIAgentAvatarDurationUsage(telemetry, log)

	content, err := marshalAIAgentSessionSummary(summary)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal the ai agent session summary: %w", err)
	}

	fileName := fmt.Sprintf("ai_agent_summary_%s-%d.json", roomSid, time.Now().UnixMilli())
	relativePath, absolutePath, err := m.buildPath(fileName, ownerRoomId, plugnmeet.RoomArtifactType_AI_AGENT_SESSION_SUMMARY)
	if err != nil {
		return nil, err
	}
	if err = os.WriteFile(absolutePath, content, 0644); err != nil {
		return nil, fmt.Errorf("failed to write the ai agent session summary file: %w", err)
	}

	fileMetadata := &plugnmeet.RoomArtifactMetadata{
		FileInfo: &plugnmeet.RoomArtifactFileInfo{
			FilePath: relativePath,
			FileSize: int64(len(content)),
			MimeType: "application/json",
		},
	}
	m.setUsageArtifactProvenance(fileMetadata, roomInfo.ID, isBreakout)
	fileMetadata.UserInfo = aiAgentSummaryUserInfo(summary)
	fileArtifact, err := m.createAndSaveArtifact(ownerRoomId, ownerRoomSid, ownerTableId, plugnmeet.RoomArtifactType_AI_AGENT_SESSION_SUMMARY, fileMetadata, true, log)
	if err != nil {
		return nil, fmt.Errorf("failed to create the ai agent session summary artifact: %w", err)
	}

	usageMetadata := m.buildAIAgentUsageMetadata(telemetry, summaryPromptTokens, summaryCompletionTokens, summarizeModel, &fileArtifact.ArtifactId, log)
	usageMetadata.UserInfo = fileMetadata.UserInfo
	m.setUsageArtifactProvenance(usageMetadata, roomInfo.ID, isBreakout)

	if _, err = m.createAndSaveArtifact(ownerRoomId, ownerRoomSid, ownerTableId, plugnmeet.RoomArtifactType_AI_AGENT_USAGE, usageMetadata, true, log); err != nil {
		log.WithError(err).Error("failed to create the ai agent usage artifact")
	}

	// The avatar of the session is billed per minute of generated video, so it
	// gets its own duration usage artifact instead of a token role.
	if avatarMetadata := m.buildAIAgentAvatarUsageMetadata(telemetry, &fileArtifact.ArtifactId, log); avatarMetadata != nil {
		avatarMetadata.UserInfo = fileMetadata.UserInfo
		m.setUsageArtifactProvenance(avatarMetadata, roomInfo.ID, isBreakout)
		if _, err = m.createAndSaveArtifact(ownerRoomId, ownerRoomSid, ownerTableId, plugnmeet.RoomArtifactType_AI_AGENT_AVATAR_USAGE, avatarMetadata, true, log); err != nil {
			log.WithError(err).Error("failed to create the ai agent avatar usage artifact")
		}
	}

	return fileArtifact, nil
}

// optionalString converts an empty string into a nil pointer so that optional
// proto fields are omitted instead of serialized as an empty value.
func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// marshalAIAgentSessionSummary renders the summary as the artifact's JSON: zero
// values, empty lists and empty maps are emitted, while unset optional fields
// stay omitted.
func marshalAIAgentSessionSummary(summary *plugnmeet.AIAgentSessionSummary) ([]byte, error) {
	opts := protojson.MarshalOptions{
		UseProtoNames:     true,
		EmitDefaultValues: true,
		Multiline:         true,
		Indent:            "  ",
	}
	return opts.Marshal(summary)
}

// aiAgentSummaryUserInfo returns the learner identity the artifacts carry. The
// identity lives on the summary itself, next to the session it belongs to.
func aiAgentSummaryUserInfo(summary *plugnmeet.AIAgentSessionSummary) *plugnmeet.RoomArtifactUserInfo {
	userInfo := summary.GetUserInfo()
	if userInfo.GetUserId() == "" {
		return nil
	}
	return userInfo
}

// Usage keys of the summary, so every cost is labelled with the role behind it.
const (
	aiAgentUsageKeySession    = "session"
	aiAgentUsageKeyDelegation = "delegation"
	aiAgentUsageKeySummary    = "summary"
	// aiAgentUsageKeyCustomProviderPrefix marks a session that ran on the host's
	// own provider and therefore carries no platform cost. The provider name is
	// appended, so billing can tell which provider served the session.
	aiAgentUsageKeyCustomProviderPrefix = "custom_provider_"
	// aiAgentUsageKeyCustomAvatarProviderPrefix does the same for an avatar the
	// room brought its own key for: the duration is recorded, never priced.
	aiAgentUsageKeyCustomAvatarProviderPrefix = "custom_avatar_provider_"
)

// aiAgentUsesCustomProvider reports whether the session ran on the host's own
// provider (BYOK/BYOP).
func aiAgentUsesCustomProvider(telemetry *redisservice.AIAgentSessionTelemetry) bool {
	return telemetry != nil && strings.TrimSpace(telemetry.CustomProvider) != ""
}

// aiAgentCustomProviderBreakdownKey names the breakdown flag of a BYOK session,
// e.g. custom_provider_google for a session served by the host's own Google
// provider.
func aiAgentCustomProviderBreakdownKey(telemetry *redisservice.AIAgentSessionTelemetry) string {
	return aiAgentUsageKeyCustomProviderPrefix + strings.ToLower(strings.TrimSpace(telemetry.CustomProvider))
}

// aiAgentUsesCustomAvatar reports whether the room brought its own avatar key,
// so the avatar provider bills the room directly.
func aiAgentUsesCustomAvatar(telemetry *redisservice.AIAgentSessionTelemetry) bool {
	return telemetry != nil && strings.TrimSpace(telemetry.CustomAvatarProvider) != ""
}

// aiAgentCustomAvatarBreakdownKey names the breakdown flag of a room-provided
// avatar, e.g. custom_avatar_provider_beyond_presence.
func aiAgentCustomAvatarBreakdownKey(telemetry *redisservice.AIAgentSessionTelemetry) string {
	provider := strings.ToLower(strings.TrimSpace(telemetry.CustomAvatarProvider))
	if provider == "" {
		return ""
	}
	return aiAgentUsageKeyCustomAvatarProviderPrefix + provider
}

// costPointer prices a usage entry, or completely omits the cost fields when the
// session ran on the host's own provider: the optional proto fields then never
// reach the JSON, and the flag in the breakdown is the only cost information.
func costPointer(cost float64, custom bool) *float64 {
	if custom {
		return nil
	}
	return roundAndPointer(cost, 6)
}

// buildAIAgentSessionUsages separates the session usage, the delegated text
// model usage and the tokens the summary synthesis spent, so each entry is
// priced at its own model rate.
func (m *ArtifactModel) buildAIAgentSessionUsages(telemetry *redisservice.AIAgentSessionTelemetry, summaryPromptTokens, summaryCompletionTokens uint32, summarizeModel string, log *logrus.Entry) map[string]*plugnmeet.RoomArtifactTokenUsage {
	session := newAIAgentSessionTokens(telemetry)
	delegation := newAIAgentDelegationTokens(telemetry)
	audioMillis := session.audioMillis
	// A room that runs on the host's own provider pays that provider directly, so
	// the tokens are recorded but the platform never prices them.
	custom := aiAgentUsesCustomProvider(telemetry)

	realtimeModel := m.aiAgentRealtimeModel(log)
	usages := make(map[string]*plugnmeet.RoomArtifactTokenUsage, 3)

	switch {
	case session.input > 0 || session.output > 0:
		// Token-based session (Realtime API, Gemini Live), priced per modality.
		var promptCost, completionCost float64
		if !custom {
			promptCost, completionCost = m.estimateModalitiesTokenCost(session.modalities(), log, aiAgentRealtimePricingKey, realtimeModel)
		}

		breakdown := session.breakdown(aiAgentUsageKeySession)
		if audioMillis > 0 {
			breakdown["audio_millis"] = int64(audioMillis)
		}

		usages[aiAgentUsageKeySession] = &plugnmeet.RoomArtifactTokenUsage{
			PromptTokens:                  clampToUint32(session.input),
			CompletionTokens:              clampToUint32(session.output),
			TotalTokens:                   clampToUint32(session.input + session.output),
			Breakdown:                     breakdown,
			PromptTokensEstimatedCost:     costPointer(promptCost, custom),
			CompletionTokensEstimatedCost: costPointer(completionCost, custom),
			TotalTokensEstimatedCost:      costPointer(promptCost+completionCost, custom),
		}

	case audioMillis > 0:
		// Minute-based Live session: the speech model reports its own audio
		// duration and delegates every text turn, so it has no tokens of its own.
		var audioCost float64
		if !custom {
			audioCost = m.estimateAudioMillisCost(insights.ServiceTypeAIAgent, audioMillis, log, aiAgentLivePricingKey, aiAgentRealtimePricingKey, realtimeModel)
		}
		usages[aiAgentUsageKeySession] = &plugnmeet.RoomArtifactTokenUsage{
			Breakdown:                map[string]int64{"audio_millis": int64(audioMillis)},
			TotalTokensEstimatedCost: costPointer(audioCost, custom),
		}
	}

	if delegation.input > 0 || delegation.output > 0 {
		// The delegated text model reports the same token detail as the session,
		// so it is normalized and priced exactly the same way.
		delegationModel := m.aiAgentDelegationModel(log)
		var promptCost, completionCost float64
		if !custom {
			promptCost, completionCost = m.estimateModalitiesTokenCost(delegation.modalities(), log, aiAgentDelegationPricingKey, delegationModel)
		}

		usages[aiAgentUsageKeyDelegation] = &plugnmeet.RoomArtifactTokenUsage{
			PromptTokens:                  clampToUint32(delegation.input),
			CompletionTokens:              clampToUint32(delegation.output),
			TotalTokens:                   clampToUint32(delegation.input + delegation.output),
			Breakdown:                     delegation.breakdown(aiAgentUsageKeyDelegation),
			PromptTokensEstimatedCost:     costPointer(promptCost, custom),
			CompletionTokensEstimatedCost: costPointer(completionCost, custom),
			TotalTokensEstimatedCost:      costPointer(promptCost+completionCost, custom),
		}
	}

	// The background rolling summaries and the end-of-session synthesis are the
	// same role, so they share one entry and one model rate.
	summaryPrompt, summaryCompletion := summaryTokensIncludingRolling(telemetry, summaryPromptTokens, summaryCompletionTokens)
	if summaryPrompt > 0 || summaryCompletion > 0 {
		var promptCost, completionCost float64
		if !custom {
			summaryModel := m.aiAgentSummaryModel(summarizeModel, log)
			promptCost = m.estimateTokensCost(insights.ServiceTypeAIAgent, summaryPrompt, true, log, aiAgentSummarizePricingKey, summaryModel)
			completionCost = m.estimateTokensCost(insights.ServiceTypeAIAgent, summaryCompletion, false, log, aiAgentSummarizePricingKey, summaryModel)
		}

		usages[aiAgentUsageKeySummary] = &plugnmeet.RoomArtifactTokenUsage{
			PromptTokens:     clampToUint32(summaryPrompt),
			CompletionTokens: clampToUint32(summaryCompletion),
			TotalTokens:      clampToUint32(summaryPrompt + summaryCompletion),
			Breakdown: map[string]int64{
				"summary_prompt_tokens":     int64(summaryPrompt),
				"summary_completion_tokens": int64(summaryCompletion),
			},
			PromptTokensEstimatedCost:     costPointer(promptCost, custom),
			CompletionTokensEstimatedCost: costPointer(completionCost, custom),
			TotalTokensEstimatedCost:      costPointer(promptCost+completionCost, custom),
		}
	}

	// The realtime avatar is not a token or speech-LLM role: it is billed per
	// minute of generated video and is recorded as its own duration usage (see
	// buildAIAgentAvatarDurationUsage), so this map stays strictly token-bound.
	return usages
}

// summaryTokensIncludingRolling combines the tokens of the end-of-session
// synthesis with the ones the background rolling summaries spent.
func summaryTokensIncludingRolling(telemetry *redisservice.AIAgentSessionTelemetry, summaryPromptTokens, summaryCompletionTokens uint32) (prompt, completion uint64) {
	prompt, completion = uint64(summaryPromptTokens), uint64(summaryCompletionTokens)
	if telemetry != nil {
		prompt += telemetry.SummaryInputTokens
		completion += telemetry.SummaryOutputTokens
	}
	return prompt, completion
}

// aiAgentSummaryModel names the model the summary tokens are priced at: the one
// that synthesized the summary, else the configured one, because a session whose
// only summaries were the rolling ones still has to be priced.
func (m *ArtifactModel) aiAgentSummaryModel(summarizeModel string, log *logrus.Entry) string {
	if model := strings.TrimSpace(summarizeModel); model != "" {
		return model
	}
	if model := m.aiAgentServiceOption(aiAgentSummarizePricingKey, log); model != "" {
		return model
	}
	return "default"
}

// Pricing keys of the ai_agent service. Like the options of the same name, every
// role can be priced directly, so operators do not have to repeat the concrete
// model name; a specific model name and the "default" entry are the fallbacks.
const (
	aiAgentLivePricingKey       = "live_model"
	aiAgentRealtimePricingKey   = "realtime_model"
	aiAgentDelegationPricingKey = "delegation_model"
	aiAgentSummarizePricingKey  = "summarize_model"
	// aiAgentAvatarPricingKey prices the avatar per minute of generated video.
	aiAgentAvatarPricingKey = "avatar_model"
)

// aiAgentServiceOption returns one ai_agent option, "" when it is not configured.
func (m *ArtifactModel) aiAgentServiceOption(key string, log *logrus.Entry) string {
	if m.app == nil || m.app.Insights == nil {
		return ""
	}
	_, service, err := m.app.Insights.GetProviderAccountForService(insights.ServiceTypeAIAgent)
	if err != nil {
		log.WithError(err).Debug("could not get the ai_agent service config for pricing")
		return ""
	}
	return strings.TrimSpace(service.GetOptionsString(key, ""))
}

// aiAgentRealtimeModel returns the model of the session itself: the live model
// when one is configured, else the realtime model, else "default".
func (m *ArtifactModel) aiAgentRealtimeModel(log *logrus.Entry) string {
	if model := m.aiAgentServiceOption("live_model", log); model != "" {
		return model
	}
	if model := m.aiAgentServiceOption("realtime_model", log); model != "" {
		return model
	}
	return "default"
}

// aiAgentDelegationModel returns the model of the delegated text backend of a
// live session.
func (m *ArtifactModel) aiAgentDelegationModel(log *logrus.Entry) string {
	if model := m.aiAgentServiceOption("delegation_model", log); model != "" {
		return model
	}
	return "default"
}

// buildAIAgentUsageMetadata combines the session usage, the delegated model
// usage and the extra tokens the summary synthesis spent into one total, each
// part priced at its own model rate.
func (m *ArtifactModel) buildAIAgentUsageMetadata(telemetry *redisservice.AIAgentSessionTelemetry, summaryPromptTokens, summaryCompletionTokens uint32, summarizeModel string, fileArtifactId *string, log *logrus.Entry) *plugnmeet.RoomArtifactMetadata {
	session := newAIAgentSessionTokens(telemetry)
	delegation := newAIAgentDelegationTokens(telemetry)
	audioMillis := session.audioMillis
	// The host's own provider bills the room directly, so no platform cost is
	// recorded; the usage stays complete otherwise.
	custom := aiAgentUsesCustomProvider(telemetry)

	realtimeModel := m.aiAgentRealtimeModel(log)
	delegationModel := m.aiAgentDelegationModel(log)

	// The rolling summaries of the session count towards the same summary role as
	// the end-of-session synthesis.
	summaryPrompt, summaryCompletion := summaryTokensIncludingRolling(telemetry, summaryPromptTokens, summaryCompletionTokens)
	totalPrompt := session.input + delegation.input + summaryPrompt
	totalCompletion := session.output + delegation.output + summaryCompletion

	var promptCost, completionCost, totalCost float64
	if !custom {
		summaryModel := m.aiAgentSummaryModel(summarizeModel, log)
		sessionPromptCost, sessionCompletionCost := m.estimateModalitiesTokenCost(session.modalities(), log, aiAgentRealtimePricingKey, realtimeModel)
		delegationPromptCost, delegationCompletionCost := m.estimateModalitiesTokenCost(delegation.modalities(), log, aiAgentDelegationPricingKey, delegationModel)
		promptCost = sessionPromptCost + delegationPromptCost +
			m.estimateTokensCost(insights.ServiceTypeAIAgent, summaryPrompt, true, log, aiAgentSummarizePricingKey, summaryModel)
		completionCost = sessionCompletionCost + delegationCompletionCost +
			m.estimateTokensCost(insights.ServiceTypeAIAgent, summaryCompletion, false, log, aiAgentSummarizePricingKey, summaryModel)

		var audioCost float64
		if session.input == 0 && session.output == 0 && audioMillis > 0 {
			audioCost = m.estimateAudioMillisCost(insights.ServiceTypeAIAgent, audioMillis, log, aiAgentLivePricingKey, aiAgentRealtimePricingKey, realtimeModel)
		}
		totalCost = promptCost + completionCost + audioCost
	}

	// Only the token roles reach this breakdown: the avatar is a per-minute
	// duration usage of its own (see buildAIAgentAvatarDurationUsage), so it is
	// never mixed into the tokens or the total cost.
	breakdown := make(map[string]int64, 32)
	for key, value := range session.breakdown(aiAgentUsageKeySession) {
		breakdown[key] = value
	}
	for key, value := range delegation.breakdown(aiAgentUsageKeyDelegation) {
		breakdown[key] = value
	}
	for key, value := range map[string]uint64{
		"audio_millis":              audioMillis,
		"summary_prompt_tokens":     summaryPrompt,
		"summary_completion_tokens": summaryCompletion,
	} {
		if value > 0 {
			breakdown[key] = int64(value)
		}
	}
	if custom {
		// The breakdown is the only free-form usage surface, so downstream
		// webhooks and billing read the flag here: this session has no platform
		// token cost, and the key names the provider that served it.
		breakdown[aiAgentCustomProviderBreakdownKey(telemetry)] = 1
	}
	return &plugnmeet.RoomArtifactMetadata{
		UsageDetails: &plugnmeet.RoomArtifactMetadata_TokenUsage{
			TokenUsage: &plugnmeet.RoomArtifactTokenUsage{
				PromptTokens:                  clampToUint32(totalPrompt),
				CompletionTokens:              clampToUint32(totalCompletion),
				TotalTokens:                   clampToUint32(totalPrompt + totalCompletion),
				Breakdown:                     breakdown,
				PromptTokensEstimatedCost:     costPointer(promptCost, custom),
				CompletionTokensEstimatedCost: costPointer(completionCost, custom),
				// A host-provided LLM hides the platform token cost entirely.
				TotalTokensEstimatedCost: costPointer(totalCost, custom),
			},
		},
		ReferenceArtifactId: fileArtifactId,
	}
}

// buildAIAgentAvatarDurationUsage prices the realtime video avatar of the
// session. The avatar is billed per minute of generated video, so it is a
// duration usage of its own rather than a token role. A room that brought its
// own avatar key pays the provider directly, so only its duration is recorded.
func (m *ArtifactModel) buildAIAgentAvatarDurationUsage(telemetry *redisservice.AIAgentSessionTelemetry, log *logrus.Entry) *plugnmeet.RoomArtifactDurationUsage {
	if telemetry == nil || telemetry.AvatarMillis == 0 {
		return nil
	}

	// The exact duration stays in the breakdown; the seconds are rounded down so
	// that anything below one second is still reported as a started second.
	durationSec := clampToUint32(telemetry.AvatarMillis / 1000)
	if durationSec == 0 {
		durationSec = 1
	}

	breakdown := map[string]int64{"avatar_millis": int64(telemetry.AvatarMillis)}
	customAvatar := aiAgentUsesCustomAvatar(telemetry)

	var avatarCost float64
	if customAvatar {
		// The avatar ran on the room's own key, so only its duration counts.
		breakdown[aiAgentCustomAvatarBreakdownKey(telemetry)] = 1
	} else {
		// Only the first configured key is used, so a concrete provider name goes
		// first: it overrides the generic role key when the operator priced the
		// providers apart, while the role key still prices everything else.
		pricingKeys := make([]string, 0, 2)
		if provider := strings.ToLower(strings.TrimSpace(telemetry.AvatarProvider)); provider != "" {
			pricingKeys = append(pricingKeys, provider)
		}
		pricingKeys = append(pricingKeys, aiAgentAvatarPricingKey)
		avatarCost = m.estimateAudioMillisCost(insights.ServiceTypeAIAgent, telemetry.AvatarMillis, log, pricingKeys...)
	}

	return &plugnmeet.RoomArtifactDurationUsage{
		DurationSec:              durationSec,
		Breakdown:                breakdown,
		DurationSecEstimatedCost: costPointer(avatarCost, customAvatar),
	}
}

// buildAIAgentAvatarUsageMetadata wraps the avatar duration usage into the
// metadata of its own artifact, nil when the session had no avatar.
func (m *ArtifactModel) buildAIAgentAvatarUsageMetadata(telemetry *redisservice.AIAgentSessionTelemetry, fileArtifactId *string, log *logrus.Entry) *plugnmeet.RoomArtifactMetadata {
	durationUsage := m.buildAIAgentAvatarDurationUsage(telemetry, log)
	if durationUsage == nil {
		return nil
	}
	return &plugnmeet.RoomArtifactMetadata{
		UsageDetails: &plugnmeet.RoomArtifactMetadata_DurationUsage{
			DurationUsage: durationUsage,
		},
		ReferenceArtifactId: fileArtifactId,
	}
}

// estimateTokensCost returns the estimated price of tokens at the rate of the
// first pricing key that is configured for the service, 0 when no pricing
// block matches.
func (m *ArtifactModel) estimateTokensCost(serviceType insights.ServiceType, tokens uint64, prompt bool, log *logrus.Entry, pricingKeys ...string) float64 {
	if tokens == 0 || m.app == nil || m.app.Insights == nil {
		return 0
	}
	pricing, err := m.app.Insights.GetServiceModelPricing(serviceType, pricingKeys...)
	if err != nil {
		log.WithError(err).Warnf("could not calculate the cost for %s with pricing keys %v", serviceType, pricingKeys)
		return 0
	}
	if prompt {
		return (float64(tokens) / 1000000) * pricing.InputPricePerMillionTokens
	}
	return (float64(tokens) / 1000000) * pricing.OutputPricePerMillionTokens
}

// aiAgentTokenCounts is the token accounting of one role: the session itself or
// the delegated text model. Both are normalized and priced the same way.
type aiAgentTokenCounts struct {
	input, output, cachedInput, cacheWriteInput uint64
	// Only the media modalities are carried: a token the provider left
	// unattributed is text, which the totals already count.
	audioInput, imageInput      uint64
	audioOutput, thinkingOutput uint64
	// The cached and the cache-write subset of every modality, as reported.
	cachedTextInput, cachedAudioInput, cachedImageInput             uint64
	cacheWriteTextInput, cacheWriteAudioInput, cacheWriteImageInput uint64
}

// clamped keeps the cached part within the input it belongs to.
func (s aiAgentTokenCounts) clamped() aiAgentTokenCounts {
	if s.cachedInput > s.input {
		s.cachedInput = s.input
	}
	return s
}

// aiAgentSessionTokens is one telemetry snapshot: the session tokens plus the
// audio duration the live model reports.
type aiAgentSessionTokens struct {
	aiAgentTokenCounts
	audioMillis uint64
}

func newAIAgentSessionTokens(telemetry *redisservice.AIAgentSessionTelemetry) aiAgentSessionTokens {
	if telemetry == nil {
		return aiAgentSessionTokens{}
	}
	return aiAgentSessionTokens{
		aiAgentTokenCounts: aiAgentTokenCounts{
			input: telemetry.InputTokens, output: telemetry.OutputTokens,
			cachedInput: telemetry.CachedInputTokens, cacheWriteInput: telemetry.CacheWriteInputTokens,
			audioInput: telemetry.AudioInputTokens, imageInput: telemetry.ImageInputTokens,
			cachedTextInput: telemetry.CachedTextInputTokens, cachedAudioInput: telemetry.CachedAudioInputTokens, cachedImageInput: telemetry.CachedImageInputTokens,
			cacheWriteTextInput: telemetry.CacheWriteTextInputTokens, cacheWriteAudioInput: telemetry.CacheWriteAudioInputTokens, cacheWriteImageInput: telemetry.CacheWriteImageInputTokens,
			audioOutput: telemetry.AudioOutputTokens, thinkingOutput: telemetry.ThinkingOutputTokens,
		}.clamped(),
		audioMillis: telemetry.AudioMillis,
	}
}

// newAIAgentDelegationTokens reads the tokens of the delegated text model, which
// is priced apart from the session model but accounted the same way.
func newAIAgentDelegationTokens(telemetry *redisservice.AIAgentSessionTelemetry) aiAgentTokenCounts {
	if telemetry == nil {
		return aiAgentTokenCounts{}
	}
	return aiAgentTokenCounts{
		input: telemetry.DelegationInputTokens, output: telemetry.DelegationOutputTokens,
		cachedInput: telemetry.DelegationCachedInputTokens, cacheWriteInput: telemetry.DelegationCacheWriteInputTokens,
		audioInput: telemetry.DelegationAudioInputTokens, imageInput: telemetry.DelegationImageInputTokens,
		cachedTextInput: telemetry.DelegationCachedTextInputTokens, cachedAudioInput: telemetry.DelegationCachedAudioInputTokens, cachedImageInput: telemetry.DelegationCachedImageInputTokens,
		cacheWriteTextInput: telemetry.DelegationCacheWriteTextInputTokens, cacheWriteAudioInput: telemetry.DelegationCacheWriteAudioInputTokens, cacheWriteImageInput: telemetry.DelegationCacheWriteImageInputTokens,
		audioOutput: telemetry.DelegationAudioOutputTokens, thinkingOutput: telemetry.DelegationThinkingOutputTokens,
	}.clamped()
}

// aiAgentModalities is the token count of every billed modality, with the cached
// and the cache-write subset of each. Both subsets are part of the totals, never
// extra, so every token is billed exactly once.
type aiAgentModalities struct {
	text, textCached, textCacheWrite    uint64
	audio, audioCached, audioCacheWrite uint64
	image, imageCached, imageCacheWrite uint64
	textOutput, audioOutput             uint64
	thinkingOutput                      uint64
}

// modalities folds the reported detail into the totals: a token the provider did
// not attribute to a modality is text, so every token is billed exactly once.
func (s aiAgentTokenCounts) modalities() aiAgentModalities {
	var m aiAgentModalities
	m.audio = min(s.audioInput, s.input)
	m.image = min(s.imageInput, s.input-m.audio)
	m.text = s.input - m.audio - m.image

	// The details can be the only cached count a provider reports, so the larger
	// of the two is used; the subtractions cannot wrap because the cached audio
	// and image counts are clamped to their own modalities below and the total
	// covers both.
	cached := max(s.cachedInput, s.cachedTextInput+s.cachedAudioInput+s.cachedImageInput)
	m.audioCached = min(s.cachedAudioInput, m.audio)
	m.imageCached = min(s.cachedImageInput, m.image)
	m.textCached = min(cached-m.audioCached-m.imageCached, m.text)

	// A cache write is billed apart from both the cached and the uncached input,
	// so it is clamped to what is left of its modality, on the same reasoning.
	cacheWrite := max(s.cacheWriteInput, s.cacheWriteTextInput+s.cacheWriteAudioInput+s.cacheWriteImageInput)
	m.audioCacheWrite = min(s.cacheWriteAudioInput, m.audio-m.audioCached)
	m.imageCacheWrite = min(s.cacheWriteImageInput, m.image-m.imageCached)
	m.textCacheWrite = min(cacheWrite-m.audioCacheWrite-m.imageCacheWrite, m.text-m.textCached)

	m.audioOutput = min(s.audioOutput, s.output)
	m.textOutput = s.output - m.audioOutput
	m.thinkingOutput = min(s.thinkingOutput, m.textOutput)
	return m
}

// breakdown reports the totals and the modality split of one role under the
// given prefix ("session" or "delegation"), omitting everything that is zero so
// a text-only usage carries no audio or image keys.
func (s aiAgentTokenCounts) breakdown(prefix string) map[string]int64 {
	m := s.modalities()
	breakdown := make(map[string]int64, 16)
	for key, value := range map[string]uint64{
		prefix + "_prompt_tokens":                   s.input,
		prefix + "_completion_tokens":               s.output,
		prefix + "_cached_prompt_tokens":            s.cachedInput,
		prefix + "_cache_write_prompt_tokens":       s.cacheWriteInput,
		prefix + "_text_prompt_tokens":              m.text,
		prefix + "_audio_prompt_tokens":             m.audio,
		prefix + "_image_prompt_tokens":             m.image,
		prefix + "_cached_text_prompt_tokens":       m.textCached,
		prefix + "_cached_audio_prompt_tokens":      m.audioCached,
		prefix + "_cached_image_prompt_tokens":      m.imageCached,
		prefix + "_cache_write_text_prompt_tokens":  m.textCacheWrite,
		prefix + "_cache_write_audio_prompt_tokens": m.audioCacheWrite,
		prefix + "_cache_write_image_prompt_tokens": m.imageCacheWrite,
		prefix + "_text_completion_tokens":          m.textOutput,
		prefix + "_audio_completion_tokens":         m.audioOutput,
		prefix + "_thinking_completion_tokens":      m.thinkingOutput,
	} {
		if value > 0 {
			breakdown[key] = int64(value)
		}
	}
	return breakdown
}

// aiAgentRates is one pricing block resolved into the rate of every modality.
type aiAgentRates struct {
	textInput, textCachedInput, textCacheWriteInput    float64
	audioInput, audioCachedInput, audioCacheWriteInput float64
	imageInput, imageCachedInput, imageCacheWriteInput float64
	textOutput, audioOutput                            float64
}

// resolveAIAgentRates resolves the rates of one pricing block. A modality without
// a rate of its own is billed as text, and a cached rate is applied only when it
// is configured: a missing cached rate never implies a discount.
func resolveAIAgentRates(pricing *config.ModelPricing) (aiAgentRates, bool) {
	if pricing == nil {
		return aiAgentRates{}, false
	}
	r := aiAgentRates{
		textInput:   pricing.InputPricePerMillionTokens,
		textOutput:  pricing.OutputPricePerMillionTokens,
		audioInput:  pricing.AudioInputPricePerMillionTokens,
		imageInput:  pricing.ImageInputPricePerMillionTokens,
		audioOutput: pricing.AudioOutputPricePerMillionTokens,
	}
	r.textCachedInput = pricing.CachedInputPricePerMillionTokens
	if r.textCachedInput <= 0 {
		r.textCachedInput = r.textInput
	}
	r.textCacheWriteInput = pricing.CacheWriteInputPricePerMillionTokens
	if r.textCacheWriteInput <= 0 {
		r.textCacheWriteInput = r.textInput
	}
	if r.audioInput <= 0 {
		r.audioInput = r.textInput
	}
	if r.imageInput <= 0 {
		r.imageInput = r.textInput
	}
	if r.audioOutput <= 0 {
		r.audioOutput = r.textOutput
	}

	// A modality with a rate of its own is discounted only by its own cached
	// rate; otherwise its cached tokens pay the uncached modality rate.
	switch {
	case pricing.AudioCachedInputPricePerMillionTokens > 0:
		r.audioCachedInput = pricing.AudioCachedInputPricePerMillionTokens
	case pricing.AudioInputPricePerMillionTokens > 0:
		r.audioCachedInput = r.audioInput
	default:
		r.audioCachedInput = r.textCachedInput
	}
	switch {
	case pricing.ImageCachedInputPricePerMillionTokens > 0:
		r.imageCachedInput = pricing.ImageCachedInputPricePerMillionTokens
	case pricing.ImageInputPricePerMillionTokens > 0:
		r.imageCachedInput = r.imageInput
	default:
		r.imageCachedInput = r.textCachedInput
	}

	// A cache write follows the same rule: its own rate, else the uncached rate
	// of its modality, else the text cache write rate.
	switch {
	case pricing.AudioCacheWriteInputPricePerMillionTokens > 0:
		r.audioCacheWriteInput = pricing.AudioCacheWriteInputPricePerMillionTokens
	case pricing.AudioInputPricePerMillionTokens > 0:
		r.audioCacheWriteInput = r.audioInput
	default:
		r.audioCacheWriteInput = r.textCacheWriteInput
	}
	switch {
	case pricing.ImageCacheWriteInputPricePerMillionTokens > 0:
		r.imageCacheWriteInput = pricing.ImageCacheWriteInputPricePerMillionTokens
	case pricing.ImageInputPricePerMillionTokens > 0:
		r.imageCacheWriteInput = r.imageInput
	default:
		r.imageCacheWriteInput = r.textCacheWriteInput
	}

	hasRate := r.textInput > 0 || r.textCachedInput > 0 || r.textOutput > 0 ||
		r.audioInput > 0 || r.imageInput > 0 || r.audioOutput > 0 || r.textCacheWriteInput > 0
	return r, hasRate
}

// cost returns the price of the prompt and of the completion tokens.
func (r aiAgentRates) cost(m aiAgentModalities) (prompt, completion float64) {
	prompt = (float64(m.text-m.textCached-m.textCacheWrite)*r.textInput +
		float64(m.textCached)*r.textCachedInput + float64(m.textCacheWrite)*r.textCacheWriteInput +
		float64(m.audio-m.audioCached-m.audioCacheWrite)*r.audioInput +
		float64(m.audioCached)*r.audioCachedInput + float64(m.audioCacheWrite)*r.audioCacheWriteInput +
		float64(m.image-m.imageCached-m.imageCacheWrite)*r.imageInput +
		float64(m.imageCached)*r.imageCachedInput + float64(m.imageCacheWrite)*r.imageCacheWriteInput) / 1000000
	completion = (float64(m.textOutput)*r.textOutput + float64(m.audioOutput)*r.audioOutput) / 1000000
	return prompt, completion
}

// estimateModalitiesTokenCost prices the tokens of one role per modality, 0 when
// no pricing block matches.
func (m *ArtifactModel) estimateModalitiesTokenCost(modalities aiAgentModalities, log *logrus.Entry, pricingKeys ...string) (prompt, completion float64) {
	if modalities.text == 0 && modalities.audio == 0 && modalities.image == 0 &&
		modalities.textOutput == 0 && modalities.audioOutput == 0 {
		return 0, 0
	}
	if m.app == nil || m.app.Insights == nil {
		return 0, 0
	}
	pricing, err := m.app.Insights.GetServiceModelPricing(insights.ServiceTypeAIAgent, pricingKeys...)
	if err != nil {
		log.WithError(err).Warnf("could not calculate the cost for %s with pricing keys %v", insights.ServiceTypeAIAgent, pricingKeys)
		return 0, 0
	}
	rates, ok := resolveAIAgentRates(pricing)
	if !ok {
		return 0, 0
	}
	return rates.cost(modalities)
}

// estimateAudioMillisCost returns the estimated price of an audio duration at
// the per-minute rate of the first pricing key that is configured for the
// service, falling back to its hourly rate.
func (m *ArtifactModel) estimateAudioMillisCost(serviceType insights.ServiceType, audioMillis uint64, log *logrus.Entry, pricingKeys ...string) float64 {
	if audioMillis == 0 || m.app == nil || m.app.Insights == nil {
		return 0
	}
	pricing, err := m.app.Insights.GetServiceModelPricing(serviceType, pricingKeys...)
	if err != nil {
		log.WithError(err).Warnf("could not calculate the audio cost for %s with pricing keys %v", serviceType, pricingKeys)
		return 0
	}
	if pricing.PricePerMinute > 0 {
		return (float64(audioMillis) / 60000.0) * pricing.PricePerMinute
	}
	if pricing.PricePerHour > 0 {
		return (float64(audioMillis) / 3600000.0) * pricing.PricePerHour
	}
	return 0
}

func clampToUint32(v uint64) uint32 {
	if v > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(v)
}
