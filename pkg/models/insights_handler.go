package models

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

func (s *InsightsModel) TranscriptionConfigure(req *plugnmeet.InsightsTranscriptionConfigReq, roomId string) error {
	metadata, err := s.natsService.GetRoomMetadataStruct(roomId)
	if err != nil {
		return err
	}

	insightsFeatures := metadata.RoomFeatures.InsightsFeatures
	if !insightsFeatures.IsAllow || !insightsFeatures.TranscriptionFeatures.IsAllow {
		return fmt.Errorf("insights feature wasn't enabled")
	}

	usersMap := make(map[string]bool)
	for _, user := range req.AllowedSpeechUsers {
		usersMap[user] = true
	}

	payload := &insights.InsightsTaskPayload{
		Task:        TaskConfigureAgent,
		ServiceType: insights.ServiceTypeTranscription,
		RoomId:      roomId,
		TargetUsers: usersMap,
		HiddenAgent: true,
	}

	err = s.ConfigureAgent(payload, 5*time.Second)
	if err != nil {
		return err
	}

	insightsFeatures.TranscriptionFeatures.IsEnabled = true
	insightsFeatures.TranscriptionFeatures.AllowedSpokenLangs = req.AllowedSpokenLangs
	insightsFeatures.TranscriptionFeatures.AllowedSpeechUsers = req.AllowedSpeechUsers
	insightsFeatures.TranscriptionFeatures.DefaultSubtitleLang = req.DefaultSubtitleLang

	if insightsFeatures.TranscriptionFeatures.IsAllowTranslation {
		insightsFeatures.TranscriptionFeatures.IsEnabledTranslation = req.IsEnabledTranslation
		insightsFeatures.TranscriptionFeatures.AllowedTransLangs = req.AllowedTransLangs
	}

	return s.natsService.UpdateAndBroadcastRoomMetadata(roomId, metadata)
}

func (s *InsightsModel) EndTranscription(roomId string) error {
	err := s.EndRoomAgentTaskByServiceName(insights.ServiceTypeTranscription, roomId, 5*time.Second)
	if err != nil {
		return err
	}
	metadata, err := s.natsService.GetRoomMetadataStruct(roomId)
	if err != nil {
		return err
	}

	metadata.RoomFeatures.InsightsFeatures.TranscriptionFeatures.IsEnabled = false
	metadata.RoomFeatures.InsightsFeatures.TranscriptionFeatures.IsEnabledTranslation = false

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
		options := insights.TranscriptionOptions{
			SpokenLang: *req.SpokenLang,
		}
		if metadata.RoomFeatures.InsightsFeatures.TranscriptionFeatures.IsEnabledTranslation {
			options.TransLangs = metadata.RoomFeatures.InsightsFeatures.TranscriptionFeatures.AllowedTransLangs
		}
		optionsBytes, err := json.Marshal(options)
		if err != nil {
			return err
		}

		var roomE2EEKey *string = nil
		if metadata.RoomFeatures.EndToEndEncryptionFeatures.IsEnabled {
			roomE2EEKey = metadata.RoomFeatures.EndToEndEncryptionFeatures.EncryptionKey
		}
		payload := &insights.InsightsTaskPayload{
			Task:        TaskUserStart,
			ServiceType: insights.ServiceTypeTranscription,
			RoomId:      roomId,
			UserId:      userId,
			Options:     optionsBytes,
			RoomE2EEKey: roomE2EEKey,
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
