package models

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	insightsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/insights"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	defaultAIContextWindow = 5
)

func (s *InsightsModel) AITextChatRequest(roomId, userId, prompt string) error {
	ctx := s.ctx
	logger := s.logger.WithField("roomId", roomId).WithField("userId", userId)

	// 1. Get service configuration & provider (SYNC)
	providerAccount, service, err := s.appConfig.Insights.GetProviderAccountForService(insights.ServiceTypeAITextChat)
	if err != nil {
		logger.WithError(err).Error("failed to get provider account")
		return err
	}
	provider, err := insightsservice.NewProvider(ctx, service.Provider, providerAccount, service, logger)
	if err != nil {
		logger.WithError(err).Error("failed to create provider")
		return err
	}

	// 2. Build history (SYNC) - now fetches only the necessary window
	history, err := s.buildHistoryWithUserPrompt(ctx, roomId, userId, prompt)
	if err != nil {
		logger.WithError(err).Error("failed to build history")
		return err
	}

	// 3. Get stream from provider
	chatModel, ok := service.Options["chat_model"].(string)
	if !ok {
		logger.Error("chat_model not configured for ai_text_chat service")
		return fmt.Errorf("chat_model not configured for ai_text_chat service")
	}

	// 4. Launch the background task ONLY after validation is successful (ASYNC)
	go func() {
		stream, err := provider.AITextChatStream(ctx, chatModel, history)
		if err != nil {
			logger.WithError(err).Error("failed to get chat stream")
			return
		}

		// Process stream
		var fullResponse strings.Builder
		var promptTokens, completionTokens, totalTokens uint32

		for res := range stream {
			if res.IsLastChunk {
				promptTokens = res.PromptTokens
				completionTokens = res.CompletionTokens
				totalTokens = res.TotalTokens
			}
			fullResponse.WriteString(res.Text)

			if marshal, err := protojson.Marshal(res); err == nil {
				err := s.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_RESP_INSIGHTS_AI_TEXT_CHAT, roomId, string(marshal), &userId)
				if err != nil {
					s.logger.WithError(err).Error("failed to broadcast system event")
				}
			}
		}

		// Append AI response to history
		aiMsg := &plugnmeet.InsightsAITextChatContent{
			Role: plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_MODEL,
			Text: fullResponse.String(),
		}
		err = s.redisService.AppendToAITextChatContext(ctx, roomId, userId, aiMsg)
		if err != nil {
			logger.WithError(err).Error("failed to append AI response to context")
		}

		// Update token usage
		err = s.redisService.UpdateAITextChatUsage(ctx, roomId, userId, insights.AITaskTypeChat, promptTokens, completionTokens, totalTokens)
		if err != nil {
			logger.WithError(err).Error("failed to update token usage")
		}

		// Trigger background summarization
		s.CheckAndSummarize(s.ctx, roomId, userId)
	}()

	return nil // Success, request accepted
}

func (s *InsightsModel) buildHistoryWithUserPrompt(ctx context.Context, roomId, userId, prompt string) ([]*plugnmeet.InsightsAITextChatContent, error) {
	var history []*plugnmeet.InsightsAITextChatContent

	// 1. Get summary
	summary, err := s.redisService.GetAITextChatSummary(ctx, roomId, userId)
	if err == nil && summary != "" {
		history = append(history, &plugnmeet.InsightsAITextChatContent{
			Role: plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_SYSTEM,
			Text: "This is a summary of the previous conversation: " + summary,
		})
	}

	// 2. Get recent context (ONLY the last N messages)
	_, service, err := s.appConfig.Insights.GetProviderAccountForService(insights.ServiceTypeAITextChat)
	if err != nil {
		return nil, err
	}

	contextWindow := float64(defaultAIContextWindow)
	if cw, ok := service.Options["context_window"].(float64); ok {
		contextWindow = cw
	}

	// Fetch only the last `context_window` messages from Redis.
	contextMessages, err := s.redisService.GetAITextChatContext(ctx, roomId, userId, -int64(contextWindow), -1)
	if err == nil {
		history = append(history, contextMessages...)
	}

	// 3. Append new user prompt
	streamId := uuid.NewString()
	userMsg := &plugnmeet.InsightsAITextChatContent{
		Role:     plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_USER,
		Text:     prompt,
		StreamId: &streamId,
	}
	history = append(history, userMsg)

	// 4. Append user prompt to the end of the full list in Redis.
	err = s.redisService.AppendToAITextChatContext(ctx, roomId, userId, userMsg)
	if err != nil {
		return nil, err
	}

	return history, nil
}

func (s *InsightsModel) CheckAndSummarize(ctx context.Context, roomId, userId string) {
	logger := s.logger.WithField("roomId", roomId).WithField("userId", userId)

	// 1. Get service config & provider
	providerAccount, service, err := s.appConfig.Insights.GetProviderAccountForService(insights.ServiceTypeAITextChat)
	if err != nil {
		logger.WithError(err).Error("failed to get provider account for summarization")
		return
	}
	provider, err := insightsservice.NewProvider(ctx, service.Provider, providerAccount, service, logger)
	if err != nil {
		logger.WithError(err).Error("failed to create provider for summarization")
		return
	}

	contextWindow := float64(defaultAIContextWindow)
	if cw, ok := service.Options["context_window"].(float64); ok {
		contextWindow = cw
	}

	// 2. Check context length
	length, err := s.redisService.GetAITextChatContextLength(ctx, roomId, userId)
	if err != nil {
		logger.WithError(err).Error("failed to get context length")
		return
	}

	if length < int64(contextWindow) {
		return // Not enough messages to summarize
	}

	logger.Info("context window reached, starting summarization")

	// 3. Get history for summarization: last summary + last N context messages
	var historyToSummarize []*plugnmeet.InsightsAITextChatContent
	summary, _ := s.redisService.GetAITextChatSummary(ctx, roomId, userId)
	if summary != "" {
		historyToSummarize = append(historyToSummarize, &plugnmeet.InsightsAITextChatContent{
			Role: plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_SYSTEM,
			Text: "This is a summary of the previous conversation: " + summary,
		})
	}
	// Fetch only the last `context_window` messages.
	contextMessages, err := s.redisService.GetAITextChatContext(ctx, roomId, userId, -int64(contextWindow), -1)
	if err != nil {
		logger.WithError(err).Error("failed to get context for summarization")
		return
	}
	historyToSummarize = append(historyToSummarize, contextMessages...)

	// 4. Call provider to summarize
	summarizeModel, _ := service.Options["summarize_model"].(string)
	newSummary, promptTokens, completionTokens, err := provider.AIChatTextSummarize(ctx, summarizeModel, historyToSummarize)
	if err != nil {
		logger.WithError(err).Error("failed to summarize chat history")
		return
	}

	// 5. Update Redis with the new summary. DO NOT DELETE CONTEXT.
	err = s.redisService.SetAITextChatSummary(ctx, roomId, userId, newSummary)
	if err != nil {
		logger.WithError(err).Error("failed to set new summary")
		return
	}

	// 6. Update token usage for the summarization task
	err = s.redisService.UpdateAITextChatUsage(ctx, roomId, userId, insights.AITaskTypeSummarize, promptTokens, completionTokens, promptTokens+completionTokens)
	if err != nil {
		logger.WithError(err).Error("failed to update token usage for summarization")
	}

	logger.Info("successfully created new summary")
}
