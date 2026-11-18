package models

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	"github.com/sirupsen/logrus"

	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

// CreateAIAgentSessionSummaryArtifact writes the summary JSON as a downloadable
// file artifact, then records the session's token usage next to it.
func (m *ArtifactModel) CreateAIAgentSessionSummaryArtifact(roomTableId uint64, roomId, roomSid string, summary *AIAgentSessionSummary, telemetry *redisservice.AIAgentSessionTelemetry, summaryPromptTokens, summaryCompletionTokens uint32, summarizeModel string, log *logrus.Entry) (*dbmodels.RoomArtifact, error) {
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

	content, err := json.MarshalIndent(summary, "", "  ")
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
	if summary.Session.UserID != "" {
		fileMetadata.UserInfo = &plugnmeet.RoomArtifactUserInfo{
			UserId: summary.Session.UserID,
			Name:   optionalString(summary.Session.UserName),
		}
	}
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

// buildAIAgentUsageMetadata combines the session's realtime usage with the extra
// tokens the summary synthesis spent.
func (m *ArtifactModel) buildAIAgentUsageMetadata(telemetry *redisservice.AIAgentSessionTelemetry, summaryPromptTokens, summaryCompletionTokens uint32, summarizeModel string, fileArtifactId *string, log *logrus.Entry) *plugnmeet.RoomArtifactMetadata {
	var sessionInput, sessionOutput uint64
	if telemetry != nil {
		sessionInput, sessionOutput = telemetry.InputTokens, telemetry.OutputTokens
	}
	totalPrompt := sessionInput + uint64(summaryPromptTokens)
	totalCompletion := sessionOutput + uint64(summaryCompletionTokens)

	realtimeModel := "default"
	if m.app.Insights != nil {
		_, service, err := m.app.Insights.GetProviderAccountForService(insights.ServiceTypeAIAgent)
		if err != nil {
			log.WithError(err).Debug("could not get the ai_agent service config for pricing")
		} else if service != nil {
			if model := service.GetOptionsString("realtime_model", ""); model != "" {
				realtimeModel = model
			}
		}
	}
	if summarizeModel == "" {
		summarizeModel = "default"
	}

	promptCost := m.estimateTokensCost(insights.ServiceTypeAIAgent, realtimeModel, sessionInput, true, log) +
		m.estimateTokensCost(insights.ServiceTypeAIAgent, summarizeModel, uint64(summaryPromptTokens), true, log)
	completionCost := m.estimateTokensCost(insights.ServiceTypeAIAgent, realtimeModel, sessionOutput, false, log) +
		m.estimateTokensCost(insights.ServiceTypeAIAgent, summarizeModel, uint64(summaryCompletionTokens), false, log)

	breakdown := make(map[string]int64, 4)
	if sessionInput > 0 {
		breakdown["session_prompt_tokens"] = int64(sessionInput)
	}
	if sessionOutput > 0 {
		breakdown["session_completion_tokens"] = int64(sessionOutput)
	}
	if summaryPromptTokens > 0 {
		breakdown["summary_prompt_tokens"] = int64(summaryPromptTokens)
	}
	if summaryCompletionTokens > 0 {
		breakdown["summary_completion_tokens"] = int64(summaryCompletionTokens)
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
				TotalTokensEstimatedCost:      roundAndPointer(promptCost+completionCost, 6),
			},
		},
		ReferenceArtifactId: fileArtifactId,
	}
}

// estimateTokensCost returns the estimated price of tokens at the model's rate,
// 0 when the pricing block is missing.
func (m *ArtifactModel) estimateTokensCost(serviceType insights.ServiceType, modelName string, tokens uint64, prompt bool, log *logrus.Entry) float64 {
	if tokens == 0 || m.app.Insights == nil {
		return 0
	}
	pricing, err := m.app.Insights.GetServiceModelPricing(serviceType, modelName)
	if err != nil {
		log.WithError(err).Warnf("could not calculate the cost for %s model %s", serviceType, modelName)
		return 0
	}
	if prompt {
		return (float64(tokens) / 1000000) * pricing.InputPricePerMillionTokens
	}
	return (float64(tokens) / 1000000) * pricing.OutputPricePerMillionTokens
}

func clampToUint32(v uint64) uint32 {
	if v > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(v)
}
