package models

import (
	"fmt"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
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

	err = s.ConfigureAgentAndWait("transcription", roomId, req.AllowedSpeechUsers, 5*time.Second)
	if err != nil {
		return err
	}

	insightsFeatures.TranscriptionFeatures.IsEnabled = true
	insightsFeatures.TranscriptionFeatures.AllowedSpokenLangs = req.AllowedSpokenLangs
	insightsFeatures.TranscriptionFeatures.AllowedSpeechUsers = req.AllowedSpeechUsers
	insightsFeatures.TranscriptionFeatures.DefaultSubtitleLang = req.DefaultSubtitleLang

	if insightsFeatures.TranscriptionFeatures.IsAllowTranslation {
		insightsFeatures.TranscriptionFeatures.IsEnabledTranslation = req.IsAllowTranslation
		insightsFeatures.TranscriptionFeatures.AllowedTransLangs = req.AllowedTransLangs
	}

	return s.natsService.UpdateAndBroadcastRoomMetadata(roomId, metadata)
}

func (s *InsightsModel) EndTranscription(roomId string) error {
	err := s.EndRoomAgentTaskByServiceNameAndWait("transcription", roomId, 5*time.Second)
	if err != nil {
		return err
	}
	metadata, err := s.natsService.GetRoomMetadataStruct(roomId)
	if err != nil {
		return err
	}

	metadata.RoomFeatures.InsightsFeatures.TranscriptionFeatures.IsEnabled = false
	metadata.RoomFeatures.InsightsFeatures.TranscriptionFeatures.IsAllowTranslation = false

	return s.natsService.UpdateAndBroadcastRoomMetadata(roomId, metadata)
}
