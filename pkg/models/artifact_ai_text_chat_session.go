package models

import (
	"fmt"
	"strings"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

// createAITextChatUsageArtifacts creates artifact records for AI text chat usage.
// It now creates separate artifacts for 'chat' and 'summarize' tasks.
func (m *ArtifactModel) createAITextChatUsageArtifacts(roomId, roomSid string, roomTableId uint64, log *logrus.Entry) error {
	// 1. Atomically get all usage data from Redis and clean up the key.
	usageMap, err := m.rs.GetAITextChatRoomUsage(m.ctx, roomId, true)
	if err != nil {
		return err
	}
	if len(usageMap) == 0 {
		return nil // No usage was recorded.
	}

	// 2. Handle the 'chat' interaction artifact.
	chatTaskType := insights.AITaskTypeChat
	chatTotalKey := fmt.Sprintf(redisservice.AiTextChatTotalTokenFields, chatTaskType)

	if totalChatTokens, ok := usageMap[chatTotalKey]; ok && totalChatTokens > 0 {
		chatPromptKey := fmt.Sprintf(redisservice.AiTextChatTotalPromptTokenFields, chatTaskType)
		chatCompletionKey := fmt.Sprintf(redisservice.AiTextChatTotalCompletionTokenFields, chatTaskType)

		// Create a breakdown map containing only chat-related keys.
		chatBreakdown := make(map[string]int64)
		for k, v := range usageMap {
			if strings.Contains(k, string(chatTaskType)) {
				chatBreakdown[k] = v
			}
		}

		metadata := &plugnmeet.RoomArtifactMetadata{
			UsageDetails: &plugnmeet.RoomArtifactMetadata_TokenUsage{
				TokenUsage: &plugnmeet.RoomArtifactTokenUsage{
					PromptTokens:     uint32(usageMap[chatPromptKey]),
					CompletionTokens: uint32(usageMap[chatCompletionKey]),
					TotalTokens:      uint32(totalChatTokens),
					Breakdown:        chatBreakdown,
				},
			},
		}
		// Create and save the artifact for chat interactions.
		err := m.createAndSaveArtifact(roomId, roomSid, roomTableId, plugnmeet.RoomArtifactType_AI_TEXT_CHAT_INTERACTION_USAGE, metadata, log)
		if err != nil {
			log.WithError(err).Error("failed to create AI text chat interaction artifact")
		}
		// 6. Add to analytics
		m.HandleAnalyticsEvent(roomId, plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_INSIGHTS_AI_TEXT_CHAT_INTERACTION_TOTAL_USAGE, nil, &totalChatTokens)
	}

	// 3. Handle the 'summarize' interaction artifact.
	summarizeTaskType := insights.AITaskTypeSummarize
	summarizeTotalKey := fmt.Sprintf(redisservice.AiTextChatTotalTokenFields, summarizeTaskType)

	if totalSummarizeTokens, ok := usageMap[summarizeTotalKey]; ok && totalSummarizeTokens > 0 {
		summarizePromptKey := fmt.Sprintf(redisservice.AiTextChatTotalPromptTokenFields, summarizeTaskType)
		summarizeCompletionKey := fmt.Sprintf(redisservice.AiTextChatTotalCompletionTokenFields, summarizeTaskType)

		// Create a breakdown map containing only summarize-related keys.
		summarizeBreakdown := make(map[string]int64)
		for k, v := range usageMap {
			if strings.Contains(k, string(summarizeTaskType)) {
				summarizeBreakdown[k] = v
			}
		}

		metadata := &plugnmeet.RoomArtifactMetadata{
			UsageDetails: &plugnmeet.RoomArtifactMetadata_TokenUsage{
				TokenUsage: &plugnmeet.RoomArtifactTokenUsage{
					PromptTokens:     uint32(usageMap[summarizePromptKey]),
					CompletionTokens: uint32(usageMap[summarizeCompletionKey]),
					TotalTokens:      uint32(totalSummarizeTokens),
					Breakdown:        summarizeBreakdown,
				},
			},
		}
		// Create and save the artifact for summarization.
		err := m.createAndSaveArtifact(roomId, roomSid, roomTableId, plugnmeet.RoomArtifactType_AI_TEXT_CHAT_SUMMARIZATION_USAGE, metadata, log)
		if err != nil {
			log.WithError(err).Error("failed to create AI text chat summarization artifact")
		}
		// 6. Add to analytics
		m.HandleAnalyticsEvent(roomId, plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_INSIGHTS_AI_TEXT_CHAT_SUMMARIZATION_TOTAL_USAGE, nil, &totalSummarizeTokens)
	}

	return nil
}
