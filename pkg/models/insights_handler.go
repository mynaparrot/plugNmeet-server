package models

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

func (s *InsightsModel) TranscriptionConfigure(req *plugnmeet.InsightsTranscriptionConfigReq, roomId string) error {
	roomInfo, metadata, err := s.natsService.GetRoomInfoWithMetadata(roomId)
	if err != nil {
		return err
	}
	if metadata == nil {
		return fmt.Errorf("empty room medata")
	}

	endToEndEncryptionFeatures := metadata.RoomFeatures.EndToEndEncryptionFeatures
	if endToEndEncryptionFeatures.EnabledSelfInsertEncryptionKey {
		return fmt.Errorf("insights.feature-disable-while-e2ee-self-key-enabled")
	}

	insightsFeatures := metadata.RoomFeatures.InsightsFeatures
	if !insightsFeatures.IsAllow || !insightsFeatures.TranscriptionFeatures.IsAllow {
		return fmt.Errorf("insights feature wasn't enabled")
	}

	insightsFeatures.TranscriptionFeatures.IsEnabled = true
	insightsFeatures.TranscriptionFeatures.AllowedSpokenLangs = req.AllowedSpokenLangs
	insightsFeatures.TranscriptionFeatures.AllowedSpeechUsers = req.AllowedSpeechUsers
	insightsFeatures.TranscriptionFeatures.DefaultSubtitleLang = req.DefaultSubtitleLang

	if insightsFeatures.TranscriptionFeatures.IsAllowTranslation {
		insightsFeatures.TranscriptionFeatures.IsEnabledTranslation = req.IsEnabledTranslation
		insightsFeatures.TranscriptionFeatures.AllowedTransLangs = req.AllowedTransLangs
	}

	if insightsFeatures.TranscriptionFeatures.IsAllowSpeechSynthesis {
		insightsFeatures.TranscriptionFeatures.IsEnabledSpeechSynthesis = req.IsEnabledSpeechSynthesis
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
		AllowedTransLangs:                  insightsFeatures.TranscriptionFeatures.AllowedTransLangs,
		EnabledTranscriptionTransSynthesis: insightsFeatures.TranscriptionFeatures.IsEnabledSpeechSynthesis,
	}

	err = s.ConfigureAgent(payload, 5*time.Second)
	if err != nil {
		return err
	}

	// analytics
	val := plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_STARTED.String()
	s.artifactModel.HandleAnalyticsEvent(roomId, plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_INSIGHTS_TRANSCRIPTION_STATUS, &val, nil)

	return s.natsService.UpdateAndBroadcastRoomMetadata(roomId, metadata)
}

func (s *InsightsModel) EndTranscription(roomId string) error {
	err := s.EndRoomAgentTaskByServiceName(insights.ServiceTypeTranscription, roomId, 5*time.Second)
	if err != nil {
		return err
	}

	// analytics
	val := plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_ENDED.String()
	s.artifactModel.HandleAnalyticsEvent(roomId, plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_INSIGHTS_TRANSCRIPTION_STATUS, &val, nil)

	return s.broadcastEndTranscription(roomId)
}

func (s *InsightsModel) broadcastEndTranscription(roomId string) error {
	metadata, err := s.natsService.GetRoomMetadataStruct(roomId)
	if err != nil {
		return err
	}
	if metadata == nil || !metadata.RoomFeatures.InsightsFeatures.TranscriptionFeatures.IsEnabled {
		// already ended or room closed
		return nil
	}

	metadata.RoomFeatures.InsightsFeatures.TranscriptionFeatures.IsEnabled = false
	metadata.RoomFeatures.InsightsFeatures.TranscriptionFeatures.IsEnabledTranslation = false
	metadata.RoomFeatures.InsightsFeatures.TranscriptionFeatures.IsEnabledSpeechSynthesis = false

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
		if metadata.RoomFeatures.EndToEndEncryptionFeatures.EnabledSelfInsertEncryptionKey {
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
		if metadata.RoomFeatures.InsightsFeatures.TranscriptionFeatures.IsEnabledTranslation {
			options.TransLangs = metadata.RoomFeatures.InsightsFeatures.TranscriptionFeatures.AllowedTransLangs
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

	msg, err := s.appConfig.NatsConn.Request(InsightsNatsChannel, p, timeout)
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

	insightsFeatures := metadata.RoomFeatures.InsightsFeatures
	if !insightsFeatures.IsAllow || !insightsFeatures.ChatTranslationFeatures.IsAllow {
		return fmt.Errorf("insights feature wasn't enabled")
	}

	if len(req.AllowedTransLangs) > int(insightsFeatures.ChatTranslationFeatures.MaxSelectedTransLangs) {
		return fmt.Errorf("max allowed selected languages exceeded")
	}

	insightsFeatures.ChatTranslationFeatures.IsEnabled = true
	insightsFeatures.ChatTranslationFeatures.AllowedTransLangs = req.AllowedTransLangs
	insightsFeatures.ChatTranslationFeatures.DefaultLang = req.DefaultLang

	// analytics
	val := plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_STARTED.String()
	s.artifactModel.HandleAnalyticsEvent(roomId, plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_INSIGHTS_CHAT_TRANSLATION_STATUS, &val, nil)

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

	insightsFeatures := metadata.RoomFeatures.InsightsFeatures
	if !insightsFeatures.IsAllow || !insightsFeatures.ChatTranslationFeatures.IsAllow {
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

	if err := s.redisService.UpdateChatTranslationUsage(ctx, roomId, userId, len(opts.Text)); err != nil {
		s.logger.WithError(err).Error("failed to update chat translation usage")
	}

	res := &plugnmeet.InsightsTranslateTextRes{
		Status: true,
		Msg:    "success",
		Result: result.(*plugnmeet.InsightsTextTranslationResult),
	}
	return res, nil
}

func (s *InsightsModel) ChatEndTranslation(roomId string) error {
	metadata, err := s.natsService.GetRoomMetadataStruct(roomId)
	if err != nil {
		return err
	}
	if metadata == nil {
		return fmt.Errorf("empty room medata")
	}

	// analytics
	val := plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_ENDED.String()
	s.artifactModel.HandleAnalyticsEvent(roomId, plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_INSIGHTS_CHAT_TRANSLATION_STATUS, &val, nil)

	metadata.RoomFeatures.InsightsFeatures.ChatTranslationFeatures.IsEnabled = false

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
	if !insightsFeatures.IsAllow || !insightsFeatures.AiFeatures.IsAllow || !insightsFeatures.AiFeatures.AiTextChatFeatures.IsAllow {
		return fmt.Errorf("insights feature wasn't enabled")
	}
	aiTextChatFeatures := insightsFeatures.AiFeatures.AiTextChatFeatures

	aiTextChatFeatures.IsEnabled = true
	aiTextChatFeatures.IsAllowedEveryone = req.IsAllowedEveryone
	aiTextChatFeatures.AllowedUserIds = req.AllowedUserIds

	// analytics
	val := plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_STARTED.String()
	s.artifactModel.HandleAnalyticsEvent(roomId, plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_INSIGHTS_AI_TEXT_CHAT_STATUS, &val, nil)

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

	insightsFeatures := metadata.RoomFeatures.InsightsFeatures
	if !insightsFeatures.IsAllow || !insightsFeatures.AiFeatures.IsAllow || !insightsFeatures.AiFeatures.AiTextChatFeatures.IsAllow {
		return fmt.Errorf("insights feature wasn't enabled")
	}
	aiTextChatFeatures := insightsFeatures.AiFeatures.AiTextChatFeatures
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
		return fmt.Errorf("you're not allowed to use this service")
	}

	return s.AITextChatRequest(roomId, userId, req.Text)
}

func (s *InsightsModel) EndAITextChat(roomId string) error {
	metadata, err := s.natsService.GetRoomMetadataStruct(roomId)
	if err != nil {
		return err
	}
	if metadata == nil {
		return fmt.Errorf("empty room medata")
	}

	// analytics
	val := plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_ENDED.String()
	s.artifactModel.HandleAnalyticsEvent(roomId, plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_INSIGHTS_AI_TEXT_CHAT_STATUS, &val, nil)

	metadata.RoomFeatures.InsightsFeatures.AiFeatures.AiTextChatFeatures.IsEnabled = false

	return s.natsService.UpdateAndBroadcastRoomMetadata(roomId, metadata)
}

func (s *InsightsModel) AIMeetingSummarizationConfig(req *plugnmeet.InsightsAIMeetingSummarizationConfigReq, roomId string) error {
	roomInfo, metadata, err := s.natsService.GetRoomInfoWithMetadata(roomId)
	if err != nil {
		return err
	}
	if roomInfo == nil {
		return fmt.Errorf("empty room medata")
	}

	endToEndEncryptionFeatures := metadata.RoomFeatures.EndToEndEncryptionFeatures
	if endToEndEncryptionFeatures.EnabledSelfInsertEncryptionKey {
		return fmt.Errorf("insights.feature-disable-while-e2ee-self-key-enabled")
	}

	insightsFeatures := metadata.RoomFeatures.InsightsFeatures
	if !insightsFeatures.IsAllow || !insightsFeatures.AiFeatures.IsAllow || !insightsFeatures.AiFeatures.MeetingSummarizationFeatures.IsAllow {
		return fmt.Errorf("insights feature wasn't enabled")
	}
	aiMeetingSummarizationFeatures := insightsFeatures.AiFeatures.MeetingSummarizationFeatures

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
		Options:                      []byte(aiMeetingSummarizationFeatures.SummarizationPrompt),
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
	val := plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_STARTED.String()
	s.artifactModel.HandleAnalyticsEvent(roomId, plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_INSIGHTS_AI_MEETING_SUMMARIZATION_STATUS, &val, nil)

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

	err = s.EndRoomAgentTaskByServiceName(insights.ServiceTypeMeetingSummarizing, roomId, 5*time.Second)
	if err != nil {
		return err
	}

	// analytics
	val := plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_ENDED.String()
	s.artifactModel.HandleAnalyticsEvent(roomId, plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_INSIGHTS_AI_MEETING_SUMMARIZATION_STATUS, &val, nil)

	metadata.RoomFeatures.InsightsFeatures.AiFeatures.MeetingSummarizationFeatures.IsEnabled = false
	return s.natsService.UpdateAndBroadcastRoomMetadata(roomId, metadata)
}
