package webhookmodel

import (
	"fmt"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/speechtotextmodel"
	log "github.com/sirupsen/logrus"
	"time"
)

func (m *WebhookModel) participantLeft(event *livekit.WebhookEvent) {
	if event.Room == nil {
		log.Errorln("empty roomInfo")
		return
	}

	_, err := m.ds.IncrementOrDecrementNumParticipants(event.Room.Sid, "-")
	if err != nil {
		log.Errorln(err)
	}
	// now we'll delete this user from active users list for this room
	_, err = m.rs.ManageActiveUsersList(event.Room.Name, event.Participant.Identity, "del", event.CreatedAt)
	if err != nil {
		log.Errorln(err)
	}

	// webhook notification
	go m.sendToWebhookNotifier(event)

	// if we missed calculating this user's speech service usage stat
	// for sudden disconnection
	sm := speechtotextmodel.New(m.app, m.ds, m.rs, m.lk)
	_ = sm.SpeechServiceUsersUsage(event.Room.Name, event.Room.Sid, event.Participant.Identity, plugnmeet.SpeechServiceUserStatusTasks_SPEECH_TO_TEXT_SESSION_ENDED)

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
		RoomId:    event.Room.Name,
		UserId:    &event.Participant.Identity,
		HsetValue: &at,
	})
}
