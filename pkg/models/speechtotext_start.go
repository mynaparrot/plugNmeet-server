package models

import (
	"fmt"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

func (m *SpeechToTextModel) SpeechToTextTranslationServiceStart(r *plugnmeet.SpeechToTextTranslationReq) error {
	if !m.app.AzureCognitiveServicesSpeech.Enabled {
		return fmt.Errorf("speech service disabled")
	}

	meta, err := m.natsService.GetRoomMetadataStruct(r.RoomId)
	if err != nil {
		return err
	}
	if meta == nil {
		return fmt.Errorf("invalid nil room metadata information")
	}

	f := meta.RoomFeatures.SpeechToTextTranslationFeatures

	f.IsEnabled = r.IsEnabled
	f.AllowedSpeechLangs = r.AllowedSpeechLangs
	f.AllowedSpeechUsers = r.AllowedSpeechUsers

	f.IsEnabledTranslation = r.IsEnabledTranslation
	f.AllowedTransLangs = r.AllowedTransLangs
	f.DefaultSubtitleLang = r.DefaultSubtitleLang

	err = m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, meta)
	if err != nil {
		return err
	}

	// send analytics
	val := plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_STARTED.String()
	d := &plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_SPEECH_SERVICE_STATUS,
		RoomId:    r.RoomId,
		HsetValue: &val,
	}
	if !f.IsEnabled {
		val = plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_ENDED.String()
		d.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_SPEECH_SERVICE_STATUS
		d.HsetValue = &val
	}
	m.analyticsModel.HandleEvent(d)

	return nil
}
