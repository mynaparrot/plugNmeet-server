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
)

// buildAIAgentSessionUsages separates the session usage, the delegated text
// model usage and the tokens the summary synthesis spent, so each entry is
// priced at its own model rate.
func (m *ArtifactModel) buildAIAgentSessionUsages(telemetry *redisservice.AIAgentSessionTelemetry, summaryPromptTokens, summaryCompletionTokens uint32, summarizeModel string, log *logrus.Entry) map[string]*plugnmeet.RoomArtifactTokenUsage {
	session := newAIAgentSessionTokens(telemetry)
	audioMillis, delegationInput, delegationOutput := session.audioMillis, session.delegationInput, session.delegationOutput

	realtimeModel := m.aiAgentRealtimeModel(log)
	usages := make(map[string]*plugnmeet.RoomArtifactTokenUsage, 3)

	switch {
	case session.input > 0 || session.output > 0:
		// Token-based session (Realtime API, Gemini Live), priced per modality.
		modalities := session.modalities()
		promptCost, completionCost := m.estimateSessionTokenCost(modalities, log, aiAgentRealtimePricingKey, realtimeModel)

		breakdown := map[string]int64{
			"session_prompt_tokens":     int64(session.input),
			"session_completion_tokens": int64(session.output),
		}
		if session.cachedInput > 0 {
			breakdown["session_cached_prompt_tokens"] = int64(session.cachedInput)
		}
		if audioMillis > 0 {
			breakdown["audio_millis"] = int64(audioMillis)
		}
		for key, value := range modalities.breakdown() {
			breakdown[key] = value
		}

		usages[aiAgentUsageKeySession] = &plugnmeet.RoomArtifactTokenUsage{
			PromptTokens:                  clampToUint32(session.input),
			CompletionTokens:              clampToUint32(session.output),
			TotalTokens:                   clampToUint32(session.input + session.output),
			Breakdown:                     breakdown,
			PromptTokensEstimatedCost:     roundAndPointer(promptCost, 6),
			CompletionTokensEstimatedCost: roundAndPointer(completionCost, 6),
			TotalTokensEstimatedCost:      roundAndPointer(promptCost+completionCost, 6),
		}

	case audioMillis > 0:
		// Minute-based Live session: the speech model reports its own audio
		// duration and delegates every text turn, so it has no tokens of its own.
		audioCost := m.estimateAudioMillisCost(insights.ServiceTypeAIAgent, audioMillis, log, aiAgentLivePricingKey, aiAgentRealtimePricingKey, realtimeModel)
		usages[aiAgentUsageKeySession] = &plugnmeet.RoomArtifactTokenUsage{
			Breakdown:                map[string]int64{"audio_millis": int64(audioMillis)},
			TotalTokensEstimatedCost: roundAndPointer(audioCost, 6),
		}
	}

	if delegationInput > 0 || delegationOutput > 0 {
		delegationModel := m.aiAgentDelegationModel(log)
		promptCost := m.estimateTokensCost(insights.ServiceTypeAIAgent, delegationInput, true, log, aiAgentDelegationPricingKey, delegationModel)
		completionCost := m.estimateTokensCost(insights.ServiceTypeAIAgent, delegationOutput, false, log, aiAgentDelegationPricingKey, delegationModel)

		usages[aiAgentUsageKeyDelegation] = &plugnmeet.RoomArtifactTokenUsage{
			PromptTokens:     clampToUint32(delegationInput),
			CompletionTokens: clampToUint32(delegationOutput),
			TotalTokens:      clampToUint32(delegationInput + delegationOutput),
			Breakdown: map[string]int64{
				"delegation_prompt_tokens":     int64(delegationInput),
				"delegation_completion_tokens": int64(delegationOutput),
			},
			PromptTokensEstimatedCost:     roundAndPointer(promptCost, 6),
			CompletionTokensEstimatedCost: roundAndPointer(completionCost, 6),
			TotalTokensEstimatedCost:      roundAndPointer(promptCost+completionCost, 6),
		}
	}

	if summaryPromptTokens > 0 || summaryCompletionTokens > 0 {
		promptCost := m.estimateTokensCost(insights.ServiceTypeAIAgent, uint64(summaryPromptTokens), true, log, aiAgentSummarizePricingKey, summarizeModel)
		completionCost := m.estimateTokensCost(insights.ServiceTypeAIAgent, uint64(summaryCompletionTokens), false, log, aiAgentSummarizePricingKey, summarizeModel)

		usages[aiAgentUsageKeySummary] = &plugnmeet.RoomArtifactTokenUsage{
			PromptTokens:     summaryPromptTokens,
			CompletionTokens: summaryCompletionTokens,
			TotalTokens:      clampToUint32(uint64(summaryPromptTokens) + uint64(summaryCompletionTokens)),
			Breakdown: map[string]int64{
				"summary_prompt_tokens":     int64(summaryPromptTokens),
				"summary_completion_tokens": int64(summaryCompletionTokens),
			},
			PromptTokensEstimatedCost:     roundAndPointer(promptCost, 6),
			CompletionTokensEstimatedCost: roundAndPointer(completionCost, 6),
			TotalTokensEstimatedCost:      roundAndPointer(promptCost+completionCost, 6),
		}
	}

	return usages
}

// Pricing keys of the ai_agent service. Like the options of the same name, every
// role can be priced directly, so operators do not have to repeat the concrete
// model name; a specific model name and the "default" entry are the fallbacks.
const (
	aiAgentLivePricingKey       = "live_model"
	aiAgentRealtimePricingKey   = "realtime_model"
	aiAgentDelegationPricingKey = "delegation_model"
	aiAgentSummarizePricingKey  = "summarize_model"
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
	audioMillis, delegationInput, delegationOutput := session.audioMillis, session.delegationInput, session.delegationOutput

	realtimeModel := m.aiAgentRealtimeModel(log)
	delegationModel := m.aiAgentDelegationModel(log)

	totalPrompt := session.input + delegationInput + uint64(summaryPromptTokens)
	totalCompletion := session.output + delegationOutput + uint64(summaryCompletionTokens)

	modalities := session.modalities()
	sessionPromptCost, sessionCompletionCost := m.estimateSessionTokenCost(modalities, log, aiAgentRealtimePricingKey, realtimeModel)
	promptCost := sessionPromptCost +
		m.estimateTokensCost(insights.ServiceTypeAIAgent, delegationInput, true, log, aiAgentDelegationPricingKey, delegationModel) +
		m.estimateTokensCost(insights.ServiceTypeAIAgent, uint64(summaryPromptTokens), true, log, aiAgentSummarizePricingKey, summarizeModel)
	completionCost := sessionCompletionCost +
		m.estimateTokensCost(insights.ServiceTypeAIAgent, delegationOutput, false, log, aiAgentDelegationPricingKey, delegationModel) +
		m.estimateTokensCost(insights.ServiceTypeAIAgent, uint64(summaryCompletionTokens), false, log, aiAgentSummarizePricingKey, summarizeModel)

	var audioCost float64
	if session.input == 0 && session.output == 0 && audioMillis > 0 {
		audioCost = m.estimateAudioMillisCost(insights.ServiceTypeAIAgent, audioMillis, log, aiAgentLivePricingKey, aiAgentRealtimePricingKey, realtimeModel)
	}
	totalCost := promptCost + completionCost + audioCost

	breakdown := make(map[string]int64, 17)
	for key, value := range map[string]uint64{
		"session_prompt_tokens":        session.input,
		"session_completion_tokens":    session.output,
		"session_cached_prompt_tokens": session.cachedInput,
		"audio_millis":                 audioMillis,
		"delegation_prompt_tokens":     delegationInput,
		"delegation_completion_tokens": delegationOutput,
		"summary_prompt_tokens":        uint64(summaryPromptTokens),
		"summary_completion_tokens":    uint64(summaryCompletionTokens),
	} {
		if value > 0 {
			breakdown[key] = int64(value)
		}
	}
	for key, value := range modalities.breakdown() {
		breakdown[key] = value
	}

	return &plugnmeet.RoomArtifactMetadata{
		UsageDetails: &plugnmeet.RoomArtifactMetadata_TokenUsage{
			TokenUsage: &plugnmeet.RoomArtifactTokenUsage{
				PromptTokens:                  clampToUint32(totalPrompt),
				CompletionTokens:              clampToUint32(totalCompletion),
				TotalTokens:                   clampToUint32(totalPrompt + totalCompletion),
				Breakdown:                     breakdown,
				PromptTokensEstimatedCost:     roundAndPointer(promptCost, 6),
				CompletionTokensEstimatedCost: roundAndPointer(completionCost, 6),
				TotalTokensEstimatedCost:      roundAndPointer(totalCost, 6),
			},
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

// aiAgentSessionTokens is one telemetry snapshot: the session totals plus the
// modality split the provider reported inside them.
type aiAgentSessionTokens struct {
	input, output, cachedInput uint64
	// Only the media modalities are carried: a token the provider left
	// unattributed is text, which the totals already count.
	audioInput, imageInput                              uint64
	cachedTextInput, cachedAudioInput, cachedImageInput uint64
	audioOutput, thinkingOutput                         uint64
	audioMillis, delegationInput, delegationOutput      uint64
}

func newAIAgentSessionTokens(telemetry *redisservice.AIAgentSessionTelemetry) aiAgentSessionTokens {
	if telemetry == nil {
		return aiAgentSessionTokens{}
	}
	tokens := aiAgentSessionTokens{
		input: telemetry.InputTokens, output: telemetry.OutputTokens, cachedInput: telemetry.CachedInputTokens,
		audioInput: telemetry.AudioInputTokens, imageInput: telemetry.ImageInputTokens,
		cachedTextInput: telemetry.CachedTextInputTokens, cachedAudioInput: telemetry.CachedAudioInputTokens, cachedImageInput: telemetry.CachedImageInputTokens,
		audioOutput: telemetry.AudioOutputTokens, thinkingOutput: telemetry.ThinkingOutputTokens,
		audioMillis:     telemetry.AudioMillis,
		delegationInput: telemetry.DelegationInputTokens, delegationOutput: telemetry.DelegationOutputTokens,
	}
	if tokens.cachedInput > tokens.input {
		tokens.cachedInput = tokens.input
	}
	return tokens
}

// aiAgentModalities is the token count of every billed modality, with the cached
// subset of each. Cached tokens are part of the totals, never extra.
type aiAgentModalities struct {
	text, textCached        uint64
	audio, audioCached      uint64
	image, imageCached      uint64
	textOutput, audioOutput uint64
	thinkingOutput          uint64
}

// modalities folds the reported detail into the totals: a token the provider did
// not attribute to a modality is text, so every token is billed exactly once.
func (s aiAgentSessionTokens) modalities() aiAgentModalities {
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

	m.audioOutput = min(s.audioOutput, s.output)
	m.textOutput = s.output - m.audioOutput
	m.thinkingOutput = min(s.thinkingOutput, m.textOutput)
	return m
}

// breakdown reports the modality counts that are not zero, so a text-only
// session carries no audio or image keys.
func (m aiAgentModalities) breakdown() map[string]int64 {
	breakdown := make(map[string]int64, 9)
	for key, value := range map[string]uint64{
		"session_text_prompt_tokens":         m.text,
		"session_audio_prompt_tokens":        m.audio,
		"session_image_prompt_tokens":        m.image,
		"session_cached_text_prompt_tokens":  m.textCached,
		"session_cached_audio_prompt_tokens": m.audioCached,
		"session_cached_image_prompt_tokens": m.imageCached,
		"session_text_completion_tokens":     m.textOutput,
		"session_audio_completion_tokens":    m.audioOutput,
		"session_thinking_completion_tokens": m.thinkingOutput,
	} {
		if value > 0 {
			breakdown[key] = int64(value)
		}
	}
	return breakdown
}

// aiAgentRates is one pricing block resolved into the rate of every modality.
type aiAgentRates struct {
	textInput, textCachedInput   float64
	audioInput, audioCachedInput float64
	imageInput, imageCachedInput float64
	textOutput, audioOutput      float64
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

	hasRate := r.textInput > 0 || r.textCachedInput > 0 || r.textOutput > 0 ||
		r.audioInput > 0 || r.imageInput > 0 || r.audioOutput > 0
	return r, hasRate
}

// cost returns the price of the prompt and of the completion tokens.
func (r aiAgentRates) cost(m aiAgentModalities) (prompt, completion float64) {
	prompt = (float64(m.text-m.textCached)*r.textInput + float64(m.textCached)*r.textCachedInput +
		float64(m.audio-m.audioCached)*r.audioInput + float64(m.audioCached)*r.audioCachedInput +
		float64(m.image-m.imageCached)*r.imageInput + float64(m.imageCached)*r.imageCachedInput) / 1000000
	completion = (float64(m.textOutput)*r.textOutput + float64(m.audioOutput)*r.audioOutput) / 1000000
	return prompt, completion
}

// estimateSessionTokenCost prices the session tokens of one snapshot per
// modality, 0 when no pricing block matches.
func (m *ArtifactModel) estimateSessionTokenCost(modalities aiAgentModalities, log *logrus.Entry, pricingKeys ...string) (prompt, completion float64) {
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
