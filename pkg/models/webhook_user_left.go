package models

import (
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	log "github.com/sirupsen/logrus"
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
	if rInfo == nil {
		log.Errorln("empty roomInfo")
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
	sm := NewSpeechToTextModel(m.app, m.ds, m.rs)
	_ = sm.SpeechServiceUsersUsage(rInfo.RoomId, rInfo.RoomSid, event.Participant.Identity, plugnmeet.SpeechServiceUserStatusTasks_SPEECH_TO_TEXT_SESSION_ENDED)
}
