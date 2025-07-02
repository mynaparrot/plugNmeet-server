package models

import (
	"fmt"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	log "github.com/sirupsen/logrus"
	"strings"
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
	event.Room.MaxParticipants = uint32(rInfo.MaxParticipants)
	event.Room.EmptyTimeout = uint32(rInfo.EmptyTimeout)

	_, err = m.ds.IncrementOrDecrementNumParticipants(rInfo.RoomSid, "+")
	if err != nil {
		log.Errorln(err)
	}

	if strings.HasPrefix(event.Participant.Identity, config.IngressUserIdPrefix) {
		// if user was ingress user then we'll have to do it manually
		// because that user will not use plugNmeet client interface
		nm := NewNatsModel(m.app, m.ds, m.rs)
		nm.OnAfterUserJoined(event.Room.Name, event.Participant.Identity)
	}

	// webhook notification
	m.sendToWebhookNotifier(event)
}

func (m *WebhookModel) participantLeft(event *livekit.WebhookEvent) {
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
	event.Room.MaxParticipants = uint32(rInfo.MaxParticipants)
	event.Room.EmptyTimeout = uint32(rInfo.EmptyTimeout)

	_, err = m.ds.IncrementOrDecrementNumParticipants(rInfo.RoomSid, "-")
	if err != nil {
		log.Errorln(err)
	}

	if strings.HasPrefix(event.Participant.Identity, config.IngressUserIdPrefix) {
		// if user was ingress user then we'll have to do it manually
		// because that user did not use plugNmeet client interface
		nm := NewNatsModel(m.app, m.ds, m.rs)
		nm.OnAfterUserDisconnected(event.Room.Name, event.Participant.Identity)
	}

	// webhook notification
	m.sendToWebhookNotifier(event)

	// if we missed calculating this user's speech service usage stat
	// for sudden disconnection
	sm := NewSpeechToTextModel(m.app, m.ds, m.rs)
	_ = sm.SpeechServiceUsersUsage(rInfo.RoomId, rInfo.RoomSid, event.Participant.Identity, plugnmeet.SpeechServiceUserStatusTasks_SPEECH_TO_TEXT_SESSION_ENDED)
}
