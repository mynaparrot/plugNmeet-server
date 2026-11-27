package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

// clampAIAgentChatText trims a participant's AI Chat message and caps it at the same bound as the server-side context injection.
func clampAIAgentChatText(text string) string {
	return truncate(strings.TrimSpace(text), aiAgentSendContextMaxRunes)
}

func (s *InsightsModel) TranscriptionConfigure(req *plugnmeet.InsightsTranscriptionConfigReq, roomId string) error {
	roomInfo, metadata, err := s.natsService.GetRoomInfoWithMetadata(roomId)
	if err != nil {
		return err
	}
	if roomInfo == nil || metadata == nil {
		return fmt.Errorf("empty room medata")
	}

	endToEndEncryptionFeatures := metadata.GetRoomFeatures().GetEndToEndEncryptionFeatures()
	if endToEndEncryptionFeatures.GetEnabledSelfInsertEncryptionKey() {
		return fmt.Errorf("insights.feature-disable-while-e2ee-self-key-enabled")
	}

	insightsFeatures := metadata.RoomFeatures.InsightsFeatures
	if !insightsFeatures.IsAllow || !insightsFeatures.TranscriptionFeatures.IsAllow {
		return fmt.Errorf("insights feature wasn't enabled")
	}
	transcriptionFeatures := insightsFeatures.TranscriptionFeatures

	transcriptionFeatures.IsEnabled = true
	transcriptionFeatures.AllowedSpokenLangs = req.AllowedSpokenLangs
	transcriptionFeatures.AllowedSpeechUsers = req.AllowedSpeechUsers
	transcriptionFeatures.DefaultSubtitleLang = req.DefaultSubtitleLang

	if transcriptionFeatures.IsAllowTranslation {
		transcriptionFeatures.IsEnabledTranslation = req.IsEnabledTranslation
		transcriptionFeatures.AllowedTransLangs = req.AllowedTransLangs
	}

	if transcriptionFeatures.IsAllowSpeechSynthesis {
		transcriptionFeatures.IsEnabledSpeechSynthesis = req.IsEnabledSpeechSynthesis
	}

	usersMap := make(map[string]bool)
	for _, user := range req.AllowedSpeechUsers {
		usersMap[user] = true
	}

	payload := &insights.InsightsTaskPayload{
		Task:                               TaskConfigureAgent,
		ServiceType:                        insights.ServiceTypeTranscription,
		RoomId:                             roomId,
		RoomTableId:                        roomInfo.DbTableId,
		RoomE2EEKey:                        endToEndEncryptionFeatures.EncryptionKey,
		TargetUsers:                        usersMap,
		HiddenAgent:                        true,
		AllowedTransLangs:                  transcriptionFeatures.AllowedTransLangs,
		EnabledTranscriptionTransSynthesis: transcriptionFeatures.IsEnabledSpeechSynthesis,
	}

	err = s.ConfigureAgent(payload, 5*time.Second)
	if err != nil {
		return err
	}

	// analytics
	s.artifactModel.HandleAnalyticsEvent(roomId, plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_INSIGHTS_TRANSCRIPTION_STATUS, new(plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_STARTED.String()), nil)

	return s.natsService.UpdateAndBroadcastRoomMetadata(roomId, metadata)
}

func (s *InsightsModel) EndTranscription(roomId string) error {
	if err := s.EndRoomAgentTaskByServiceName(insights.ServiceTypeTranscription, roomId, 5*time.Second); err != nil {
		return err
	}

	return s.broadcastEndTranscription(roomId)
}

func (s *InsightsModel) broadcastEndTranscription(roomId string) error {
	metadata, err := s.natsService.GetRoomMetadataStruct(roomId)
	if err != nil {
		return err
	}
	if metadata == nil {
		// already ended or room closed
		return nil
	}

	tf := metadata.RoomFeatures.InsightsFeatures.GetTranscriptionFeatures()
	if tf == nil || !tf.IsEnabled {
		// already ended or room closed
		return nil
	}

	tf.IsEnabled = false
	tf.IsEnabledTranslation = false
	tf.IsEnabledSpeechSynthesis = false

	// analytics
	s.artifactModel.HandleAnalyticsEvent(roomId, plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_INSIGHTS_TRANSCRIPTION_STATUS, new(plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_ENDED.String()), nil)

	return s.natsService.UpdateAndBroadcastRoomMetadata(roomId, metadata)
}

func (s *InsightsModel) TranscriptionUserSession(req *plugnmeet.InsightsTranscriptionUserSessionReq, roomId, userId string) error {
	if req.Action == plugnmeet.InsightsUserSessionAction_USER_SESSION_ACTION_START {
		if req.SpokenLang == nil || *req.SpokenLang == "" {
			return fmt.Errorf("spoken lang is required")
		}

		metadata, err := s.natsService.GetRoomMetadataStruct(roomId)
		if err != nil {
			return err
		}
		if metadata == nil {
			return fmt.Errorf("empty room medata")
		}

		endToEndEncryptionFeatures := metadata.GetRoomFeatures().GetEndToEndEncryptionFeatures()
		if endToEndEncryptionFeatures.GetEnabledSelfInsertEncryptionKey() {
			return fmt.Errorf("insights.feature-disable-while-e2ee-self-key-enabled")
		}

		userInfo, err := s.natsService.GetUserInfo(roomId, userId)
		if err != nil {
			return err
		}
		if userInfo == nil {
			return fmt.Errorf("empty user info")
		}

		options := insights.TranscriptionOptions{
			SpokenLang:                  *req.SpokenLang,
			UserName:                    userInfo.Name,
			AllowedTranscriptionStorage: req.AllowedTranscriptionStorage,
		}

		transcriptionFeatures := metadata.RoomFeatures.InsightsFeatures.GetTranscriptionFeatures()
		if transcriptionFeatures.GetIsEnabledTranslation() {
			options.TransLangs = transcriptionFeatures.GetAllowedTransLangs()
		}
		optionsBytes, err := json.Marshal(options)
		if err != nil {
			return err
		}

		payload := &insights.InsightsTaskPayload{
			Task:        TaskUserStart,
			ServiceType: insights.ServiceTypeTranscription,
			RoomId:      roomId,
			UserId:      userId,
			Options:     optionsBytes,
		}

		err = s.ActivateAgentTaskForUser(payload, time.Second*5)
		if err != nil {
			return err
		}
		return nil

	} else if req.Action == plugnmeet.InsightsUserSessionAction_USER_SESSION_ACTION_STOP {
		payload := &insights.InsightsTaskPayload{
			Task:        TaskUserEnd,
			ServiceType: insights.ServiceTypeTranscription,
			RoomId:      roomId,
			UserId:      userId,
		}
		err := s.EndAgentTaskForUser(payload, time.Second*5)
		if err != nil {
			return err
		}
		return nil
	}

	return fmt.Errorf("unknown action '%s'", req.Action.String())
}

// GetUserTaskStatus sends a request to the leader agent and waits for the user's task status.
func (s *InsightsModel) GetUserTaskStatus(serviceType insights.ServiceType, roomId, userId string, timeout time.Duration) ([]byte, error) {
	payload := &insights.InsightsTaskPayload{
		Task:        TaskGetUserStatus,
		ServiceType: serviceType,
		RoomId:      roomId,
		UserId:      userId,
	}
	p, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	msg, err := s.natsConn.Request(insights.InsightsNatsChannel, p, timeout)
	if err != nil {
		return nil, fmt.Errorf("NATS request for user status failed: %w", err)
	}

	return msg.Data, nil
}

func (s *InsightsModel) ChatTranslationConfigure(req *plugnmeet.InsightsChatTranslationConfigReq, roomId string) error {
	metadata, err := s.natsService.GetRoomMetadataStruct(roomId)
	if err != nil {
		return err
	}
	if metadata == nil {
		return fmt.Errorf("empty room medata")
	}

	insightsFeatures := metadata.GetRoomFeatures().GetInsightsFeatures()
	chatTranslationFeatures := insightsFeatures.GetChatTranslationFeatures()
	if !insightsFeatures.GetIsAllow() || !chatTranslationFeatures.GetIsAllow() {
		return fmt.Errorf("insights feature wasn't enabled")
	}

	if len(req.AllowedTransLangs) > int(chatTranslationFeatures.GetMaxSelectedTransLangs()) {
		return fmt.Errorf("max allowed selected languages exceeded")
	}

	chatTranslationFeatures.IsEnabled = true
	chatTranslationFeatures.AllowedTransLangs = req.AllowedTransLangs
	chatTranslationFeatures.DefaultLang = req.DefaultLang

	// analytics
	s.artifactModel.HandleAnalyticsEvent(roomId, plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_INSIGHTS_CHAT_TRANSLATION_STATUS, new(plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_STARTED.String()), nil)

	return s.natsService.UpdateAndBroadcastRoomMetadata(roomId, metadata)
}

func (s *InsightsModel) ExecuteChatTranslation(ctx context.Context, req *plugnmeet.InsightsTranslateTextReq, roomId, userId string) (*plugnmeet.InsightsTranslateTextRes, error) {
	metadata, err := s.natsService.GetRoomMetadataStruct(roomId)
	if err != nil {
		return nil, err
	}
	if metadata == nil {
		return nil, fmt.Errorf("empty room medata")
	}

	insightsFeatures := metadata.GetRoomFeatures().GetInsightsFeatures()
	chatTranslationFeatures := insightsFeatures.GetChatTranslationFeatures()
	if !insightsFeatures.GetIsAllow() || !chatTranslationFeatures.GetIsAllow() || !chatTranslationFeatures.GetIsEnabled() {
		return nil, fmt.Errorf("feature wasn't enabled")
	}

	opts := insights.TranslationTaskOptions{
		Text:        req.Text,
		SourceLang:  req.SourceLang,
		TargetLangs: req.TargetLangs,
	}
	options, err := json.Marshal(opts)
	if err != nil {
		return nil, err
	}

	result, err := s.ActivateTextTask(ctx, insights.ServiceTypeTranslation, options)
	if err != nil {
		return nil, err
	}

	charCount := utf8.RuneCountInString(opts.Text)
	if err := s.redisService.UpdateChatTranslationUsage(ctx, roomId, userId, charCount); err != nil {
		s.logger.WithError(err).Error("failed to update chat translation usage")
	}
	s.addLiveChatTranslationCost(ctx, roomId, charCount)

	res := &plugnmeet.InsightsTranslateTextRes{
		Status: true,
		Msg:    "success",
		Result: result.(*plugnmeet.InsightsTextTranslationResult),
	}
	return res, nil
}

// addLiveChatTranslationCost adds the price of the translated characters to the
// room's live cost; a service without a pricing block books nothing.
func (s *InsightsModel) addLiveChatTranslationCost(ctx context.Context, roomId string, charCount int) {
	if charCount <= 0 {
		return
	}
	pricing, err := s.appConfig.Insights.GetServiceModelPricing(insights.ServiceTypeTranslation, "default")
	if err != nil || pricing.PricePerMillionCharacters <= 0 {
		return
	}
	delta := (float64(charCount) / 1000000.0) * pricing.PricePerMillionCharacters
	if err := s.redisService.IncrInsightsLiveCost(ctx, roomId, redisservice.LiveCostFieldChatTranslation, delta); err != nil {
		s.logger.WithError(err).Error("failed to update chat translation live cost")
	}
}

func (s *InsightsModel) ChatEndTranslation(roomId string) error {
	metadata, err := s.natsService.GetRoomMetadataStruct(roomId)
	if err != nil {
		return err
	}
	if metadata == nil {
		return fmt.Errorf("empty room medata")
	}

	chatTranslationFeatures := metadata.RoomFeatures.InsightsFeatures.GetChatTranslationFeatures()
	if chatTranslationFeatures == nil || !chatTranslationFeatures.IsEnabled {
		return nil
	}

	// analytics
	s.artifactModel.HandleAnalyticsEvent(roomId, plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_INSIGHTS_CHAT_TRANSLATION_STATUS, new(plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_ENDED.String()), nil)

	chatTranslationFeatures.IsEnabled = false

	return s.natsService.UpdateAndBroadcastRoomMetadata(roomId, metadata)
}

func (s *InsightsModel) AITextChatConfigure(req *plugnmeet.InsightsAITextChatConfigReq, roomId string) error {
	metadata, err := s.natsService.GetRoomMetadataStruct(roomId)
	if err != nil {
		return err
	}
	if metadata == nil {
		return fmt.Errorf("empty room medata")
	}

	insightsFeatures := metadata.RoomFeatures.InsightsFeatures
	aiTextChatFeatures := insightsFeatures.GetAiFeatures().GetAiTextChatFeatures()
	if !insightsFeatures.IsAllow || !insightsFeatures.GetAiFeatures().GetIsAllow() || !aiTextChatFeatures.GetIsAllow() {
		// The client translates the sentinel key; log the full sentence here.
		s.logger.WithField("roomId", roomId).Warn("AI text chat features are not enabled for this room")
		return fmt.Errorf("insights.ai-text-chat.feature-disabled")
	}

	aiTextChatFeatures.IsEnabled = true
	aiTextChatFeatures.IsAllowedEveryone = req.IsAllowedEveryone
	aiTextChatFeatures.AllowedUserIds = req.AllowedUserIds
	aiTextChatFeatures.IsNotepadAiDisabled = req.IsNotepadAiDisabled
	aiTextChatFeatures.IsWhiteboardAiDisabled = req.IsWhiteboardAiDisabled
	aiTextChatFeatures.IsPollAiDisabled = req.IsPollAiDisabled

	// analytics
	s.artifactModel.HandleAnalyticsEvent(roomId, plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_INSIGHTS_AI_TEXT_CHAT_STATUS, new(plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_STARTED.String()), nil)

	return s.natsService.UpdateAndBroadcastRoomMetadata(roomId, metadata)
}

func (s *InsightsModel) ExecuteAITextChat(req *plugnmeet.InsightsAITextChatContent, roomId, userId string) error {
	metadata, err := s.natsService.GetRoomMetadataStruct(roomId)
	if err != nil {
		return err
	}
	if metadata == nil {
		return fmt.Errorf("empty room medata")
	}

	insightsFeatures := metadata.GetRoomFeatures().GetInsightsFeatures()

	requestFrom := req.GetRequestFrom()
	// A running AI agent owns the AI Chat box of its own 1-on-1 session room, so
	// the participant's message goes to the agent's live session instead of the text-chat provider.
	if shouldHandleAIAgentChat(metadata, requestFrom) {
		return s.handleAIAgentChatMessage(req, insightsFeatures.GetAiAgentFeatures(), roomId, userId)
	}

	aiFeatures := insightsFeatures.GetAiFeatures()
	aiTextChatFeatures := aiFeatures.GetAiTextChatFeatures()
	if !insightsFeatures.GetIsAllow() || !aiFeatures.GetIsAllow() || !aiTextChatFeatures.GetIsAllow() || !aiTextChatFeatures.GetIsEnabled() {
		// The client translates the sentinel key; log the full sentence here.
		s.logger.WithField("roomId", roomId).WithField("userId", userId).Warn("AI text chat features are not enabled for this room")
		return fmt.Errorf("insights.ai-text-chat.feature-disabled")
	}
	foundUser := aiTextChatFeatures.IsAllowedEveryone

	if !aiTextChatFeatures.IsAllowedEveryone {
		for _, id := range aiTextChatFeatures.AllowedUserIds {
			if id == userId {
				foundUser = true
				break
			}
		}
	}

	if !foundUser {
		// The client translates the sentinel key; log the full sentence here.
		s.logger.WithField("roomId", roomId).WithField("userId", userId).Warn("user is not allowed to use AI text chat")
		return fmt.Errorf("insights.ai-text-chat.not-allowed")
	}

	if requestFrom == plugnmeet.InsightsAIRequestSource_INSIGHTS_AI_REQUEST_SOURCE_NOTEPAD &&
		aiTextChatFeatures.IsNotepadAiDisabled {
		// The client translates the sentinel key; log the full sentence here.
		s.logger.WithField("roomId", roomId).WithField("userId", userId).Warn("notepad AI is disabled")
		return fmt.Errorf("insights.notepad-ai.errors.ai-disabled")
	}
	if requestFrom == plugnmeet.InsightsAIRequestSource_INSIGHTS_AI_REQUEST_SOURCE_WHITEBOARD &&
		aiTextChatFeatures.IsWhiteboardAiDisabled {
		// The client translates the sentinel key; log the full sentence here.
		s.logger.WithField("roomId", roomId).WithField("userId", userId).Warn("whiteboard AI is disabled")
		return fmt.Errorf("insights.whiteboard-ai.errors.ai-disabled")
	}
	if requestFrom == plugnmeet.InsightsAIRequestSource_INSIGHTS_AI_REQUEST_SOURCE_POLL &&
		aiTextChatFeatures.IsPollAiDisabled {
		// The client translates the sentinel key; log the full sentence here.
		s.logger.WithField("roomId", roomId).WithField("userId", userId).Warn("poll AI is disabled")
		return fmt.Errorf("polls.errors.ai-disabled")
	}

	var streamId string
	if req.StreamId != nil {
		streamId = *req.StreamId
	}

	return s.AITextChatRequest(roomId, userId, req.Text, streamId, requestFrom)
}

// shouldHandleAIAgentChat reports whether an AI Chat request belongs to the room's running AI agent.
// Only a 1-on-1 agent session room owns the AI Chat box, the same check the client mirrors in
// isAIAgentSessionRoom: a group room and the parent lobby of per-user sessions inherit ai_agent_features.is_allow
// but never run an agent, so their text chat keeps using the configured provider.
func shouldHandleAIAgentChat(meta *plugnmeet.RoomMetadata, requestFrom plugnmeet.InsightsAIRequestSource) bool {
	return requestFrom == plugnmeet.InsightsAIRequestSource_INSIGHTS_AI_REQUEST_SOURCE_CHAT &&
		meta.GetRoomFeatures().GetInsightsFeatures().GetIsAllow() &&
		isOneOnOneAIAgentRoom(meta)
}

// handleAIAgentChatMessage passes a participant's AI Chat message to the room's
// running AI agent. The agent answers by voice and, when useful, with a written
// chat message of its own, so only the caller's own stream is closed here.
func (s *InsightsModel) handleAIAgentChatMessage(req *plugnmeet.InsightsAITextChatContent, aiAgentFeatures *plugnmeet.InsightsAIAgentFeatures, roomId, userId string) error {
	logger := s.logger.WithField("roomId", roomId).WithField("userId", userId)

	if !aiAgentFeatures.GetAllowChatMessage() {
		logger.Warn("the AI agent is not allowed to answer in the AI Chat box")
		return fmt.Errorf("insights.ai-text-chat.feature-disabled")
	}

	// a 1-on-1 room only takes turns from its named participants; an observer uses public chat instead
	if allowed := aiAgentAllowedParticipantIds(aiAgentFeatures); len(allowed) > 0 && !containsUserId(allowed, userId) {
		logger.Warn("user is not allowed to talk to the AI agent")
		return config.ErrAIAgentUserNotAllowed
	}

	// the session may run on any node of the cluster, so only its shared state decides
	if s.aiAgentModel == nil || !s.aiAgentModel.IsAgentActive(s.ctx, roomId) {
		logger.Warn("the AI agent is not running in this room")
		return fmt.Errorf("ai-agent.errors.session-not-started")
	}

	userName := userId
	if userInfo, _ := s.natsService.GetUserInfo(roomId, userId); userInfo.GetName() != "" {
		userName = userInfo.GetName()
	}
	err := s.aiAgentModel.ForwardParticipantChatMessage(s.ctx, roomId, userId, userName, clampAIAgentChatText(req.GetText()))
	if err != nil {
		if errors.Is(err, config.ErrAIAgentNotRunning) {
			logger.Warn("the AI agent is not running in this room")
			return fmt.Errorf("ai-agent.errors.session-not-started")
		}
		logger.WithError(err).Error("failed to pass the chat message to the AI agent")
		return err
	}

	// The client keeps its request pending until the last chunk of its stream id
	// arrives; the agent's answer comes back as its own chat event.
	streamId := req.GetStreamId()
	if streamId == "" {
		return nil
	}
	res := &plugnmeet.InsightsAITextChatStreamResult{
		Id:          streamId,
		IsLastChunk: true,
		CreatedAt:   fmt.Sprintf("%d", time.Now().UnixMilli()),
		RequestFrom: plugnmeet.InsightsAIRequestSource_INSIGHTS_AI_REQUEST_SOURCE_CHAT.Enum(),
	}
	if raw, err := protojson.Marshal(res); err == nil {
		_ = s.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_RESP_INSIGHTS_AI_TEXT_CHAT, roomId, string(raw), &userId)
	}

	return nil
}

func (s *InsightsModel) EndAITextChat(roomId string) error {
	metadata, err := s.natsService.GetRoomMetadataStruct(roomId)
	if err != nil {
		return err
	}
	if metadata == nil {
		return fmt.Errorf("empty room medata")
	}

	aiTextChatFeatures := metadata.RoomFeatures.InsightsFeatures.GetAiFeatures().GetAiTextChatFeatures()
	if aiTextChatFeatures == nil || !aiTextChatFeatures.IsEnabled {
		return nil
	}

	// analytics
	s.artifactModel.HandleAnalyticsEvent(roomId, plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_INSIGHTS_AI_TEXT_CHAT_STATUS, new(plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_ENDED.String()), nil)

	aiTextChatFeatures.IsEnabled = false

	return s.natsService.UpdateAndBroadcastRoomMetadata(roomId, metadata)
}

func (s *InsightsModel) AIMeetingSummarizationConfig(req *plugnmeet.InsightsAIMeetingSummarizationConfigReq, roomId string) error {
	roomInfo, metadata, err := s.natsService.GetRoomInfoWithMetadata(roomId)
	if err != nil {
		return err
	}
	if roomInfo == nil || metadata == nil {
		return fmt.Errorf("empty room medata")
	}

	endToEndEncryptionFeatures := metadata.GetRoomFeatures().GetEndToEndEncryptionFeatures()
	if endToEndEncryptionFeatures.GetEnabledSelfInsertEncryptionKey() {
		return fmt.Errorf("insights.feature-disable-while-e2ee-self-key-enabled")
	}
	insightsFeatures := metadata.GetRoomFeatures().GetInsightsFeatures()
	aiFeatures := insightsFeatures.GetAiFeatures()
	aiMeetingSummarizationFeatures := aiFeatures.GetMeetingSummarizationFeatures()
	if !insightsFeatures.GetIsAllow() || !aiFeatures.GetIsAllow() || !aiMeetingSummarizationFeatures.GetIsAllow() {
		return fmt.Errorf("insights feature wasn't enabled")
	}

	aiMeetingSummarizationFeatures.IsEnabled = true
	aiMeetingSummarizationFeatures.SummarizationPrompt = req.SummarizationPrompt

	payload := &insights.InsightsTaskPayload{
		Task:                         TaskConfigureAgent,
		ServiceType:                  insights.ServiceTypeMeetingSummarizing,
		RoomId:                       roomId,
		RoomTableId:                  roomInfo.DbTableId,
		RoomE2EEKey:                  endToEndEncryptionFeatures.EncryptionKey,
		CaptureAllParticipantsTracks: true,
		HiddenAgent:                  true,
		Options:                      []byte(aiMeetingSummarizationFeatures.GetSummarizationPrompt()),
	}

	err = s.ConfigureAgent(payload, 5*time.Second)
	if err != nil {
		return err
	}

	err = s.natsService.UpdateAndBroadcastRoomMetadata(roomId, metadata)
	if err != nil {
		return err
	}

	// analytics
	s.artifactModel.HandleAnalyticsEvent(roomId, plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_INSIGHTS_AI_MEETING_SUMMARIZATION_STATUS, new(plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_STARTED.String()), nil)

	// notify everyone about this
	return s.natsService.BroadcastSystemNotificationToRoom(roomId, "insights.meeting-summarization.enabled-notification-all", plugnmeet.NatsSystemNotificationTypes_NATS_SYSTEM_NOTIFICATION_INFO, true, nil)
}

func (s *InsightsModel) EndEndAIMeetingSummarization(roomId string) error {
	metadata, err := s.natsService.GetRoomMetadataStruct(roomId)
	if err != nil {
		return err
	}
	if metadata == nil {
		return fmt.Errorf("empty room medata")
	}
	if !metadata.RoomFeatures.InsightsFeatures.GetAiFeatures().GetMeetingSummarizationFeatures().GetIsEnabled() {
		return nil
	}

	if err := s.EndRoomAgentTaskByServiceName(insights.ServiceTypeMeetingSummarizing, roomId, 5*time.Second); err != nil {
		return err
	}

	return s.broadcastEndAIMeetingSummarization(roomId)
}

func (s *InsightsModel) broadcastEndAIMeetingSummarization(roomId string) error {
	metadata, err := s.natsService.GetRoomMetadataStruct(roomId)
	if err != nil {
		return err
	}
	if metadata == nil {
		return fmt.Errorf("empty room medata")
	}

	meetingSummarizationFeatures := metadata.RoomFeatures.InsightsFeatures.GetAiFeatures().GetMeetingSummarizationFeatures()
	if meetingSummarizationFeatures == nil || !meetingSummarizationFeatures.IsEnabled {
		return nil
	}

	// analytics
	s.artifactModel.HandleAnalyticsEvent(roomId, plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_INSIGHTS_AI_MEETING_SUMMARIZATION_STATUS, new(plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_ENDED.String()), nil)

	meetingSummarizationFeatures.IsEnabled = false
	return s.natsService.UpdateAndBroadcastRoomMetadata(roomId, metadata)
}

// StopRoomInsightsServices stops every active insights service in a running room, disables the insights flags in
// the room metadata, and harvests the usage artifacts accumulated so far without closing the room itself.
func (s *InsightsModel) StopRoomInsightsServices(ctx context.Context, roomId, roomSid string, dbTableId uint64) {
	log := s.logger.WithFields(logrus.Fields{
		"room_id":       roomId,
		"room_sid":      roomSid,
		"room_table_id": dbTableId,
		"method":        "StopRoomInsightsServices",
	})

	if err := s.EndRoomAllAgentTasks(roomId); err != nil {
		log.WithError(err).Warn("failed to stop insights room agents")
	}

	_ = s.broadcastEndTranscription(roomId)
	_ = s.ChatEndTranslation(roomId)
	_ = s.EndAITextChat(roomId)
	_ = s.broadcastEndAIMeetingSummarization(roomId)

	// If an AI agent session ran in this room and was not an auto-closing per-user breakout room,
	// finalize its summary and usage artifact now.
	s.aiAgentModel.FinalizeSession(ctx, dbTableId, roomId, roomSid)

	// Harvest all remaining usage artifacts and clear the room's live cost key.
	s.artifactModel.CreateAllRoomUsageArtifacts(roomId, roomSid, dbTableId, log)
}
