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
// It now creates separate artifacts for 'chat' and 'summarize' tasks with cost calculation.
func (m *ArtifactModel) createAITextChatUsageArtifacts(roomId, roomSid string, roomTableId uint64, log *logrus.Entry) error {
	// 1. Atomically get all usage data from Redis and clean up the key.
	usageMap, err := m.rs.GetAITextChatRoomUsage(m.ctx, roomId, true)
	if err != nil {
		return err
	}
	if len(usageMap) == 0 {
		return nil // No usage was recorded.
	}

	// Get the service config to find model names
	_, service, err := m.app.Insights.GetProviderAccountForService(insights.ServiceTypeAITextChat)
	if err != nil {
		log.WithError(err).Error("could not get service config for ai_text_chat")
		// Continue without pricing if config is missing
	}

	// 2. Handle the 'chat' interaction artifact.
	chatTaskType := insights.AITaskTypeChat
	chatTotalKey := fmt.Sprintf(redisservice.AiTextChatTotalTokenFields, chatTaskType)

	if totalChatTokens, ok := usageMap[chatTotalKey]; ok && totalChatTokens > 0 {
		promptTokens := uint32(usageMap[fmt.Sprintf(redisservice.AiTextChatTotalPromptTokenFields, chatTaskType)])
		completionTokens := uint32(usageMap[fmt.Sprintf(redisservice.AiTextChatTotalCompletionTokenFields, chatTaskType)])

		// Calculate cost for the chat model
		var promptCost, completionCost, totalCost float64
		chatModel := "default" // Fallback model name
		if service != nil && service.Options != nil {
			if model, ok := service.Options["chat_model"].(string); ok {
				chatModel = model
			}
		}
		pricing, err := m.app.Insights.GetServiceModelPricing(insights.ServiceTypeAITextChat, chatModel)
		if err == nil {
			promptCost = (float64(promptTokens) / 1000000) * pricing.InputPricePerMillionTokens
			completionCost = (float64(completionTokens) / 1000000) * pricing.OutputPricePerMillionTokens
			totalCost = promptCost + completionCost
		} else {
			log.WithError(err).Warnf("could not calculate cost for ai_text_chat model %s", chatModel)
		}

		chatBreakdown := make(map[string]int64)
		for k, v := range usageMap {
			if strings.Contains(k, string(chatTaskType)) {
				chatBreakdown[k] = v
			}
		}

		metadata := &plugnmeet.RoomArtifactMetadata{
			UsageDetails: &plugnmeet.RoomArtifactMetadata_TokenUsage{
				TokenUsage: &plugnmeet.RoomArtifactTokenUsage{
					PromptTokens:                  promptTokens,
					CompletionTokens:              completionTokens,
					TotalTokens:                   uint32(totalChatTokens),
					Breakdown:                     chatBreakdown,
					PromptTokensEstimatedCost:     roundAndPointer(promptCost, 6),
					CompletionTokensEstimatedCost: roundAndPointer(completionCost, 6),
					TotalTokensEstimatedCost:      roundAndPointer(totalCost, 6),
				},
			},
		}
		_, err = m.createAndSaveArtifact(roomId, roomSid, roomTableId, plugnmeet.RoomArtifactType_AI_TEXT_CHAT_INTERACTION_USAGE, metadata, log)
		if err != nil {
			log.WithError(err).Error("failed to create AI text chat interaction artifact")
		}
		m.HandleAnalyticsEvent(roomId, plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_INSIGHTS_AI_TEXT_CHAT_INTERACTION_TOTAL_USAGE, nil, &totalChatTokens)
	}

	// 3. Handle the 'summarize' interaction artifact.
	summarizeTaskType := insights.AITaskTypeSummarize
	summarizeTotalKey := fmt.Sprintf(redisservice.AiTextChatTotalTokenFields, summarizeTaskType)

	if totalSummarizeTokens, ok := usageMap[summarizeTotalKey]; ok && totalSummarizeTokens > 0 {
		promptTokens := uint32(usageMap[fmt.Sprintf(redisservice.AiTextChatTotalPromptTokenFields, summarizeTaskType)])
		completionTokens := uint32(usageMap[fmt.Sprintf(redisservice.AiTextChatTotalCompletionTokenFields, summarizeTaskType)])

		// Calculate cost for the summarize model
		var promptCost, completionCost, totalCost float64
		summarizeModel := "default" // Fallback model name
		if service != nil && service.Options != nil {
			if model, ok := service.Options["summarize_model"].(string); ok {
				summarizeModel = model
			}
		}
		pricing, err := m.app.Insights.GetServiceModelPricing(insights.ServiceTypeAITextChat, summarizeModel)
		if err == nil {
			promptCost = (float64(promptTokens) / 1000000) * pricing.InputPricePerMillionTokens
			completionCost = (float64(completionTokens) / 1000000) * pricing.OutputPricePerMillionTokens
			totalCost = promptCost + completionCost
		} else {
			log.WithError(err).Warnf("could not calculate cost for ai_text_chat model %s", summarizeModel)
		}

		summarizeBreakdown := make(map[string]int64)
		for k, v := range usageMap {
			if strings.Contains(k, string(summarizeTaskType)) {
				summarizeBreakdown[k] = v
			}
		}

		metadata := &plugnmeet.RoomArtifactMetadata{
			UsageDetails: &plugnmeet.RoomArtifactMetadata_TokenUsage{
				TokenUsage: &plugnmeet.RoomArtifactTokenUsage{
					PromptTokens:                  promptTokens,
					CompletionTokens:              completionTokens,
					TotalTokens:                   uint32(totalSummarizeTokens),
					Breakdown:                     summarizeBreakdown,
					PromptTokensEstimatedCost:     roundAndPointer(promptCost, 6),
					CompletionTokensEstimatedCost: roundAndPointer(completionCost, 6),
					TotalTokensEstimatedCost:      roundAndPointer(totalCost, 6),
				},
			},
		}
		_, err = m.createAndSaveArtifact(roomId, roomSid, roomTableId, plugnmeet.RoomArtifactType_AI_TEXT_CHAT_SUMMARIZATION_USAGE, metadata, log)
		if err != nil {
			log.WithError(err).Error("failed to create AI text chat summarization artifact")
		}
		m.HandleAnalyticsEvent(roomId, plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_INSIGHTS_AI_TEXT_CHAT_SUMMARIZATION_TOTAL_USAGE, nil, &totalSummarizeTokens)
	}

	return nil
}
