package webhookmodel

import (
	"fmt"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/speechtotext"
	log "github.com/sirupsen/logrus"
	"time"
)

func (m *WebhookModel) participantLeft(event *livekit.WebhookEvent) {
	if event.Room == nil {
		log.Errorln("empty roomInfo")
		return
	}

	rInfo, err := m.natsService.GetRoomInfo(event.Room.Name)
	if err != nil {
		log.Errorln(err)
		return
	}
	event.Room.Sid = rInfo.RoomSid
	event.Room.Metadata = rInfo.Metadata

	_, err = m.ds.IncrementOrDecrementNumParticipants(rInfo.RoomSid, "-")
	if err != nil {
		log.Errorln(err)
	}

	// webhook notification
	go m.sendToWebhookNotifier(event)

	// if we missed calculating this user's speech service usage stat
	// for sudden disconnection
	sm := speechtotextmodel.New(m.app, m.ds, m.rs, m.lk)
	_ = sm.SpeechServiceUsersUsage(rInfo.RoomId, rInfo.RoomSid, event.Participant.Identity, plugnmeet.SpeechServiceUserStatusTasks_SPEECH_TO_TEXT_SESSION_ENDED)

	// send analytics
	at := fmt.Sprintf("%d", time.Now().UnixMilli())
	if event.GetCreatedAt() > 0 {
		// sometime events send in unordered way, so better to use when it was created
		// otherwise will give invalid data, for backward compatibility convert to milliseconds
		at = fmt.Sprintf("%d", event.GetCreatedAt()*1000)
	}
	m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_USER,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_LEFT,
		RoomId:    rInfo.RoomId,
		UserId:    &event.Participant.Identity,
		HsetValue: &at,
	})
}
