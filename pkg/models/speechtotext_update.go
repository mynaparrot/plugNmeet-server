package models

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

func (m *SpeechToTextModel) SpeechServiceUsersUsage(roomId, rSid, userId string, task plugnmeet.SpeechServiceUserStatusTasks) error {
	switch task {
	case plugnmeet.SpeechServiceUserStatusTasks_SPEECH_TO_TEXT_SESSION_STARTED:
		_, err := m.rs.SpeechToTextUsersUsage(roomId, userId, task)
		if err != nil {
			return err
		}
		// webhook
		m.sendToWebhookNotifier(roomId, rSid, &userId, task, 0)
		// send analytics
		val := plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_STARTED.String()
		m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
			EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_USER,
			EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_SPEECH_SERVICES_STATUS,
			RoomId:    roomId,
			UserId:    &userId,
			HsetValue: &val,
		})
	case plugnmeet.SpeechServiceUserStatusTasks_SPEECH_TO_TEXT_SESSION_ENDED:
		if usage, err := m.rs.SpeechToTextUsersUsage(roomId, userId, task); err == nil && usage > 0 {
			// send webhook
			m.sendToWebhookNotifier(roomId, rSid, &userId, task, usage)
			// send analytics
			val := plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_ENDED.String()
			m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
				EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_USER,
				EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_SPEECH_SERVICES_STATUS,
				RoomId:    roomId,
				UserId:    &userId,
				HsetValue: &val,
			})
			// another to record total usage
			m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
				EventType:         plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_USER,
				EventName:         plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_SPEECH_SERVICES_USAGE,
				RoomId:            roomId,
				UserId:            &userId,
				EventValueInteger: &usage,
			})
		}
	}

	// now remove this user from the request list
	_, _ = m.rs.SpeechToTextAzureKeyRequestedTask(roomId, userId, "remove")
	return nil
}

func (m *SpeechToTextModel) SpeechServiceUserStatus(r *plugnmeet.SpeechServiceUserStatusReq) error {
	err := m.rs.SpeechToTextUpdateUserStatus(r.KeyId, r.Task)
	if err != nil {
		return err
	}

	return m.SpeechServiceUsersUsage(r.RoomId, r.RoomSid, r.UserId, r.Task)
}
