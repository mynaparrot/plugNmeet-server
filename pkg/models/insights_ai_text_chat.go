package models

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	insightsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/insights"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/insights/mcp"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cast"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	defaultAIContextWindow = 5
)

func (s *InsightsModel) AITextChatRequest(roomId, userId, prompt, streamId string, requestFrom plugnmeet.InsightsAIRequestSource) error {
	ctx := s.ctx
	logger := s.logger.WithField("roomId", roomId).WithField("userId", userId)

	// 1. Get service configuration & provider (SYNC)
	providerAccount, service, err := s.appConfig.Insights.GetProviderAccountForService(insights.ServiceTypeAITextChat)
	if err != nil {
		logger.WithError(err).Error("failed to get provider account")
		return err
	}
	args := &insightsservice.ProviderArgs{
		Ctx:             ctx,
		ProviderType:    service.Provider,
		ProviderAccount: providerAccount,
		ServiceConfig:   service,
		RDS:             s.rds,
		Logger:          logger,
	}
	provider, err := insightsservice.NewProvider(args)
	if err != nil {
		logger.WithError(err).Error("failed to create provider")
		return err
	}

	// 2. Build history (SYNC) - now fetches only the necessary window.
	// Notepad/Whiteboard/Poll AI requests use a fresh, ephemeral history and
	// never touch the per-user chat conversation context.
	isEphemeral := requestFrom == plugnmeet.InsightsAIRequestSource_INSIGHTS_AI_REQUEST_SOURCE_NOTEPAD ||
		requestFrom == plugnmeet.InsightsAIRequestSource_INSIGHTS_AI_REQUEST_SOURCE_WHITEBOARD ||
		requestFrom == plugnmeet.InsightsAIRequestSource_INSIGHTS_AI_REQUEST_SOURCE_POLL
	var history []*plugnmeet.InsightsAITextChatContent
	if isEphemeral {
		history = []*plugnmeet.InsightsAITextChatContent{
			{
				Role:     plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_USER,
				Text:     prompt,
				StreamId: &streamId,
			},
		}
	} else {
		history, err = s.buildHistoryWithUserPrompt(ctx, roomId, userId, prompt)
		if err != nil {
			logger.WithError(err).Error("failed to build history")
			return err
		}
	}

	// 3. Get stream from provider
	chatModel, ok := service.Options["chat_model"].(string)
	if !ok {
		logger.Error("chat_model not configured for ai_text_chat service")
		return fmt.Errorf("chat_model not configured for ai_text_chat service")
	}

	// 4. Only a room administrator may run the room-wide tool set.
	var toolRunner *insights.AITextChatToolRunner
	if !isEphemeral && requestFrom == plugnmeet.InsightsAIRequestSource_INSIGHTS_AI_REQUEST_SOURCE_CHAT {
		userInfo, _ := s.natsService.GetUserInfo(roomId, userId)
		if s.shouldAttachAdminTools(requestFrom, isEphemeral, userInfo) {
			toolRunner = s.adminTextChatToolRunner(ctx, roomId, userId, logger)
		}
	}

	// 5. Launch the background task ONLY after validation is successful (ASYNC)
	go func() {
		stream, err := textChatStream(ctx, provider, chatModel, history, toolRunner)
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

			if streamId != "" {
				res.Id = streamId
			}
			res.RequestFrom = &requestFrom

			if marshal, err := protojson.Marshal(res); err == nil {
				err := s.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_RESP_INSIGHTS_AI_TEXT_CHAT, roomId, string(marshal), &userId)
				if err != nil {
					s.logger.WithError(err).Error("failed to broadcast system event")
				}
			}
		}

		if !isEphemeral {
			// Append AI response to history
			aiMsg := &plugnmeet.InsightsAITextChatContent{
				Role: plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_MODEL,
				Text: fullResponse.String(),
			}
			if err := s.redisService.AppendToAITextChatContext(ctx, roomId, userId, aiMsg); err != nil {
				logger.WithError(err).Error("failed to append AI response to context")
			}
		}

		// An ephemeral request spends real tokens too, so its usage and cost are
		// always recorded; only the context and the summary are chat-only.
		s.recordAITextChatUsageAndCost(ctx, roomId, userId, insights.AITaskTypeChat, chatModel, promptTokens, completionTokens, totalTokens)

		if isEphemeral {
			return
		}

		// Trigger background summarization
		s.CheckAndSummarize(s.ctx, roomId, userId)
	}()

	return nil // Success, request accepted
}

// shouldAttachAdminTools reports whether an AI text chat request may run the
// room-wide admin tool set: only a regular chat request of a room administrator
// while the AI agent model is wired.
func (s *InsightsModel) shouldAttachAdminTools(requestFrom plugnmeet.InsightsAIRequestSource, isEphemeral bool, userInfo *plugnmeet.NatsKvUserInfo) bool {
	return !isEphemeral &&
		requestFrom == plugnmeet.InsightsAIRequestSource_INSIGHTS_AI_REQUEST_SOURCE_CHAT &&
		userInfo.GetIsAdmin() &&
		s.aiAgentModel != nil
}

// adminTextChatToolRunner builds the tool set of an administrator's AI text chat
// session: the room, whiteboard, notepad, poll, breakout, insights and external
// MCP tools, with the browser side reaching the administrator's own client.
func (s *InsightsModel) adminTextChatToolRunner(ctx context.Context, roomId, userId string, logger *logrus.Entry) *insights.AITextChatToolRunner {
	if s.aiAgentModel == nil {
		return nil
	}
	deps := s.BuildToolDependencies(roomId, userId, logger)
	// The external servers are discovered when the chat is configured; this only
	// warms what is still missing, so a request never waits on a remote server.
	mcp.EnsureRoomExternalMCP(ctx, s.redisService, roomId, nil, 0, logger, deps.ExternalURLHook)

	reg := mcp.BuildAdminTextChatToolRegistry(s.aiAgentModel.roomAgentFeatures(roomId), deps)
	exec := mcp.NewExecutor(mcp.ExecutorOptions{
		Registry: reg,
		Client:   deps.Client,
	})

	return &insights.AITextChatToolRunner{
		Tools: reg.Specs(),
		Execute: func(ctx context.Context, callID, name, argsJSON string) (string, bool) {
			result := exec.Execute(ctx, mcp.ToolCall{
				CallID:   callID,
				RoomID:   roomId,
				ToolName: name,
				ArgsJSON: argsJSON,
			})
			return result.OutputJSON, result.IsError
		},
	}
}

// adminTextChatToolsPrompt is added to the provider call of an administrator's
// AI text chat. The quiz rule keeps a test from reaching the participants before
// the administrator has seen and approved it.
const adminTextChatToolsPrompt = "You are the assistant of the room administrator and you can act on the live room " +
	"through your tools: room settings and lock state, whiteboard, notepad, polls, breakout rooms, insights " +
	"and any attached external MCP server. Use them instead of telling the administrator to do the work " +
	"manually, and only for what was asked. When external MCP tools (tools starting with ext_, such as a " +
	"question bank, lesson PDF/book pages, or documentation server) or fetch_external_knowledge are " +
	"available, treat them as your first-priority authoritative source of truth: always query the relevant " +
	"ext_* tool first before answering questions about product features, technical details, documentation, " +
	"or lesson material, and before drafting quizzes, keeping strictly within any specified chapter or page " +
	"range instead of relying on pre-training memory. Whenever a quiz is requested: ground the questions in " +
	"the room's active context — check connected ext_* question-bank or lesson/documentation tools first when " +
	"available, and when the request relates to the room's ongoing activity or gives no explicit topic, also " +
	"consider the active whiteboard (whiteboard_capture_snapshot), existing polls (get_poll_results), the " +
	"meeting title and welcome message (get_room_info), or the shared notepad (call notepad_read only if the user " +
	"refers to the notepad or no topic/MCP/whiteboard source is available); if the requested quiz topic differs from the " +
	"session's main subject (for example HTML in a PHP session), connect the questions to both (e.g. PHP + " +
	"HTML) so they stay anchored to the session, then call draft_quiz so the " +
	"questions, the options and the correct answers are previewed in this chat, and wait for the " +
	"administrator's explicit approval before calling publish_quiz. For a multi-question quiz, pass all " +
	"questions in one draft_quiz call using the `questions` array; if you draft them one at a time, every " +
	"single-question draft is added to the room's pending draft. To publish all pending drafted questions, " +
	"call publish_quiz with draft_id \"latest\" or omit draft_id. Never publish a quiz whose draft has not " +
	"been approved. For Speech-to-Text, first establish whom it is for: specific participant names or user " +
	"ids, or everyone. After you configure it for the requested user(s) or for everyone, always remind the " +
	"user to click the \"T\" icon in the bottom menu bar, select their spoken language, and click Start, " +
	"because transcription does not start automatically. When asked to use the whiteboard (or the shared " +
	"notepad), never switch to the other surface just because a tool call failed once while opening; retry " +
	"the requested surface's tool once instead. On the whiteboard, every tool call creates one visual group and " +
	"returns groupId and anchorElementId: group related explanatory cards/notes into a single " +
	"whiteboard_write_content call, and place items that belong side-by-side (such as explanation notes beside a " +
	"diagram) with layout=\"right_of_element\" plus anchor_element_id set to the other group's anchorElementId or " +
	"groupId (for a follow-up you may also omit anchor_element_id or pass \"last\", which anchors to the group " +
	"drawn most recently). Use layout=\"free_space\" for a new or unrelated topic/section so it starts below existing content " +
	"with clean spacing instead of beside an unrelated group. In the shared notepad, never use # (H1) headings — use " +
	"### (H3) or ## (H2) for section titles and **bold** for sub-labels."

// textChatStream starts the provider stream, using the tool calling variant when
// an admin runner is present and the provider supports tools.
func textChatStream(ctx context.Context, provider insights.Provider, chatModel string, history []*plugnmeet.InsightsAITextChatContent, runner *insights.AITextChatToolRunner) (<-chan *plugnmeet.InsightsAITextChatStreamResult, error) {
	if runner != nil {
		if toolProvider, ok := provider.(insights.ToolTextChatProvider); ok {
			return toolProvider.AITextChatStreamWithTools(ctx, chatModel, withAdminTextChatToolsPrompt(history), runner)
		}
	}
	return provider.AITextChatStream(ctx, chatModel, history)
}

// withAdminTextChatToolsPrompt returns a copy of history with the tool
// instructions prepended. The stored chat history is never mutated.
func withAdminTextChatToolsPrompt(history []*plugnmeet.InsightsAITextChatContent) []*plugnmeet.InsightsAITextChatContent {
	out := make([]*plugnmeet.InsightsAITextChatContent, 0, len(history)+1)
	out = append(out, &plugnmeet.InsightsAITextChatContent{
		Role: plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_SYSTEM,
		Text: adminTextChatToolsPrompt,
	})

	return append(out, history...)
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
	if cw, ok := service.Options["context_window"]; ok {
		contextWindow = cast.ToFloat64(cw)
	}

	// Fetch only the last `context_window` messages from Redis.
	contextMessages, err := s.redisService.GetAITextChatContext(ctx, roomId, userId, -int64(contextWindow), -1)
	if err == nil {
		history = append(history, contextMessages...)
	}

	// 3. Append new user prompt
	userMsg := &plugnmeet.InsightsAITextChatContent{
		Role:     plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_USER,
		Text:     prompt,
		StreamId: new(uuid.NewString()),
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
	args := &insightsservice.ProviderArgs{
		Ctx:             ctx,
		ProviderType:    service.Provider,
		ProviderAccount: providerAccount,
		ServiceConfig:   service,
		RDS:             s.rds,
		Logger:          logger,
	}
	provider, err := insightsservice.NewProvider(args)
	if err != nil {
		logger.WithError(err).Error("failed to create provider for summarization")
		return
	}

	contextWindow := float64(defaultAIContextWindow)
	if cw, ok := service.Options["context_window"]; ok {
		contextWindow = cast.ToFloat64(cw)
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
	s.recordAITextChatUsageAndCost(ctx, roomId, userId, insights.AITaskTypeSummarize, summarizeModel, promptTokens, completionTokens, promptTokens+completionTokens)

	logger.Info("successfully created new summary")
}

// recordAITextChatUsageAndCost books the tokens of one AI text chat call and adds
// their price to the room's live cost.
func (s *InsightsModel) recordAITextChatUsageAndCost(ctx context.Context, roomId, userId string, taskType insights.AITaskType, modelName string, promptTokens, completionTokens, totalTokens uint32) {
	if err := s.redisService.UpdateAITextChatUsage(ctx, roomId, userId, taskType, promptTokens, completionTokens, totalTokens); err != nil {
		s.logger.WithError(err).Error("failed to update ai text chat usage")
	}

	pricing, err := s.appConfig.Insights.GetServiceModelPricing(insights.ServiceTypeAITextChat, modelName)
	if err != nil {
		return
	}
	delta := (float64(promptTokens)/1000000.0)*pricing.InputPricePerMillionTokens +
		(float64(completionTokens)/1000000.0)*pricing.OutputPricePerMillionTokens
	if delta <= 0 {
		return
	}
	if err := s.redisService.IncrInsightsLiveCost(ctx, roomId, redisservice.LiveCostFieldAITextChat, delta); err != nil {
		s.logger.WithError(err).Error("failed to update ai text chat live cost")
	}
}
