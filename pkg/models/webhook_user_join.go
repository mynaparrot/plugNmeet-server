package models

import (
	"fmt"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	log "github.com/sirupsen/logrus"
	"time"
)

func (m *WebhookModel) participantJoined(event *livekit.WebhookEvent) {
	if event.Room == nil {
		log.Errorln("empty roomInfo")
		return
	}

	rInfo, err := m.natsService.GetRoomInfo(event.Room.Name)
	if err != nil {
		log.Errorln(err)
		return
	}
	if rInfo == nil {
		log.Errorln("empty roomInfo")
		return
	}

	event.Room.Sid = rInfo.RoomSid
	event.Room.Metadata = rInfo.Metadata

	_, err = m.ds.IncrementOrDecrementNumParticipants(rInfo.RoomSid, "+")
	if err != nil {
		log.Errorln(err)
	}

	// webhook notification
	go m.sendToWebhookNotifier(event)

	// send analytics
	at := fmt.Sprintf("%d", time.Now().UnixMilli())
	if event.GetCreatedAt() > 0 {
		// sometime events send in unordered way, so better to use when it was created
		// otherwise will give invalid data, for backward compatibility convert to milliseconds
		at = fmt.Sprintf("%d", event.GetCreatedAt()*1000)
	}
	m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_JOINED,
		RoomId:    event.Room.Name,
		UserId:    &event.Participant.Identity,
		UserName:  &event.Participant.Name,
		ExtraData: &event.Participant.Metadata,
		HsetValue: &at,
	})
}
