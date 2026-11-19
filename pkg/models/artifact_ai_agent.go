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

// buildAIAgentSessionUsages separates the session usage, the delegated text
// model usage and the tokens the summary synthesis spent, so each entry is
// priced at its own model rate.
func (m *ArtifactModel) buildAIAgentSessionUsages(telemetry *redisservice.AIAgentSessionTelemetry, summaryPromptTokens, summaryCompletionTokens uint32, summarizeModel string, log *logrus.Entry) []*plugnmeet.RoomArtifactTokenUsage {
	var sessionInput, sessionOutput, audioMillis, delegationInput, delegationOutput uint64
	if telemetry != nil {
		sessionInput, sessionOutput = telemetry.InputTokens, telemetry.OutputTokens
		audioMillis = telemetry.AudioMillis
		delegationInput, delegationOutput = telemetry.DelegationInputTokens, telemetry.DelegationOutputTokens
	}

	realtimeModel := m.aiAgentRealtimeModel(log)
	usages := make([]*plugnmeet.RoomArtifactTokenUsage, 0, 3)

	switch {
	case sessionInput > 0 || sessionOutput > 0:
		// Token-based session (Realtime API, Gemini Live).
		promptCost := m.estimateTokensCost(insights.ServiceTypeAIAgent, sessionInput, true, log, aiAgentRealtimePricingKey, realtimeModel)
		completionCost := m.estimateTokensCost(insights.ServiceTypeAIAgent, sessionOutput, false, log, aiAgentRealtimePricingKey, realtimeModel)

		breakdown := map[string]int64{
			"session_prompt_tokens":     int64(sessionInput),
			"session_completion_tokens": int64(sessionOutput),
		}
		if audioMillis > 0 {
			breakdown["audio_millis"] = int64(audioMillis)
		}

		usages = append(usages, &plugnmeet.RoomArtifactTokenUsage{
			PromptTokens:                  clampToUint32(sessionInput),
			CompletionTokens:              clampToUint32(sessionOutput),
			TotalTokens:                   clampToUint32(sessionInput + sessionOutput),
			Breakdown:                     breakdown,
			PromptTokensEstimatedCost:     roundAndPointer(promptCost, 6),
			CompletionTokensEstimatedCost: roundAndPointer(completionCost, 6),
			TotalTokensEstimatedCost:      roundAndPointer(promptCost+completionCost, 6),
		})

	case audioMillis > 0:
		// Minute-based Live session: the speech model reports its own audio
		// duration and delegates every text turn, so it has no tokens of its own.
		audioCost := m.estimateAudioMillisCost(insights.ServiceTypeAIAgent, audioMillis, log, aiAgentLivePricingKey, aiAgentRealtimePricingKey, realtimeModel)
		usages = append(usages, &plugnmeet.RoomArtifactTokenUsage{
			Breakdown:                map[string]int64{"audio_millis": int64(audioMillis)},
			TotalTokensEstimatedCost: roundAndPointer(audioCost, 6),
		})
	}

	if delegationInput > 0 || delegationOutput > 0 {
		delegationModel := m.aiAgentDelegationModel(log)
		promptCost := m.estimateTokensCost(insights.ServiceTypeAIAgent, delegationInput, true, log, aiAgentDelegationPricingKey, delegationModel)
		completionCost := m.estimateTokensCost(insights.ServiceTypeAIAgent, delegationOutput, false, log, aiAgentDelegationPricingKey, delegationModel)

		usages = append(usages, &plugnmeet.RoomArtifactTokenUsage{
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
		})
	}

	if summaryPromptTokens > 0 || summaryCompletionTokens > 0 {
		promptCost := m.estimateTokensCost(insights.ServiceTypeAIAgent, uint64(summaryPromptTokens), true, log, aiAgentSummarizePricingKey, summarizeModel)
		completionCost := m.estimateTokensCost(insights.ServiceTypeAIAgent, uint64(summaryCompletionTokens), false, log, aiAgentSummarizePricingKey, summarizeModel)

		usages = append(usages, &plugnmeet.RoomArtifactTokenUsage{
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
		})
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
	var sessionInput, sessionOutput, audioMillis, delegationInput, delegationOutput uint64
	if telemetry != nil {
		sessionInput, sessionOutput = telemetry.InputTokens, telemetry.OutputTokens
		audioMillis = telemetry.AudioMillis
		delegationInput, delegationOutput = telemetry.DelegationInputTokens, telemetry.DelegationOutputTokens
	}

	realtimeModel := m.aiAgentRealtimeModel(log)
	delegationModel := m.aiAgentDelegationModel(log)

	totalPrompt := sessionInput + delegationInput + uint64(summaryPromptTokens)
	totalCompletion := sessionOutput + delegationOutput + uint64(summaryCompletionTokens)

	promptCost := m.estimateTokensCost(insights.ServiceTypeAIAgent, sessionInput, true, log, aiAgentRealtimePricingKey, realtimeModel) +
		m.estimateTokensCost(insights.ServiceTypeAIAgent, delegationInput, true, log, aiAgentDelegationPricingKey, delegationModel) +
		m.estimateTokensCost(insights.ServiceTypeAIAgent, uint64(summaryPromptTokens), true, log, aiAgentSummarizePricingKey, summarizeModel)
	completionCost := m.estimateTokensCost(insights.ServiceTypeAIAgent, sessionOutput, false, log, aiAgentRealtimePricingKey, realtimeModel) +
		m.estimateTokensCost(insights.ServiceTypeAIAgent, delegationOutput, false, log, aiAgentDelegationPricingKey, delegationModel) +
		m.estimateTokensCost(insights.ServiceTypeAIAgent, uint64(summaryCompletionTokens), false, log, aiAgentSummarizePricingKey, summarizeModel)

	var audioCost float64
	if sessionInput == 0 && sessionOutput == 0 && audioMillis > 0 {
		audioCost = m.estimateAudioMillisCost(insights.ServiceTypeAIAgent, audioMillis, log, aiAgentLivePricingKey, aiAgentRealtimePricingKey, realtimeModel)
	}
	totalCost := promptCost + completionCost + audioCost

	breakdown := make(map[string]int64, 7)
	for key, value := range map[string]uint64{
		"session_prompt_tokens":        sessionInput,
		"session_completion_tokens":    sessionOutput,
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
