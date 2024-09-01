package models

import (
	"fmt"
	"github.com/livekit/protocol/livekit"
	log "github.com/sirupsen/logrus"
)

func (m *WebhookModel) participantJoined(event *livekit.WebhookEvent) {
	if event.Room == nil {
		log.Warnln(fmt.Sprintf("invalid webhook info received: %+v", event))
		return
	}

	rInfo, err := m.natsService.GetRoomInfo(event.Room.Name)
	if err != nil || rInfo == nil {
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
}
