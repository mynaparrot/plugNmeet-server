package models

import (
	"strings"
	"time"

	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/sirupsen/logrus"
)

func (m *WebhookModel) participantJoined(event *livekit.WebhookEvent) {
	if event.Room == nil || event.Participant == nil {
		m.logger.Warnln("received participant_joined webhook with nil room or participant info")
		return
	}

	log := m.logger.WithFields(logrus.Fields{
		"roomId":        event.Room.Name,
		"participantId": event.Participant.Identity,
		"event":         event.GetEvent(),
	})
	log.Infoln("handling participant_joined webhook")

	rInfo, err := m.natsService.GetRoomInfo(event.Room.Name)
	if err != nil {
		log.WithError(err).Errorln("failed to get room info from NATS")
		return
	}
	if rInfo == nil {
		log.Warnln("room not found in NATS, skipping participant_joined tasks")
		return
	}

	event.Room.Sid = rInfo.RoomSid
	event.Room.Metadata = rInfo.Metadata
	event.Room.MaxParticipants = uint32(rInfo.MaxParticipants)
	event.Room.EmptyTimeout = uint32(rInfo.EmptyTimeout)

	_, err = m.ds.IncrementOrDecrementNumParticipants(rInfo.RoomSid, "+")
	if err != nil {
		log.WithError(err).Errorln("error incrementing num participants")
	}

	if strings.HasPrefix(event.Participant.Identity, config.IngressUserIdPrefix) || strings.HasPrefix(event.Participant.Identity, config.TTSAgentUserIdPrefix) {
		// if user was internal agent user then we'll have to do it manually
		// because that user will not use plugNmeet client interface
		log.Infof("internal agent participant joined, triggering OnAfterUserJoined manually")
		m.nm.OnAfterUserJoined(event.Room.Name, event.Participant.Identity)
	}

	// webhook notification
	m.sendToWebhookNotifier(event)
	log.Info("successfully processed participant_joined webhook")
}

func (m *WebhookModel) participantLeft(event *livekit.WebhookEvent) {
	if event.Room == nil || event.Participant == nil {
		m.logger.Warnln("received participant_left webhook with nil room or participant info")
		return
	}

	log := m.logger.WithFields(logrus.Fields{
		"roomId":        event.Room.Name,
		"participantId": event.Participant.Identity,
		"event":         event.GetEvent(),
	})
	log.Infoln("handling participant_left webhook")

	rInfo, err := m.natsService.GetRoomInfo(event.Room.Name)
	if err != nil {
		log.WithError(err).Errorln("failed to get room info from NATS")
		return
	}
	if rInfo == nil {
		log.Warnln("room not found in NATS, skipping participant_left tasks")
		return
	}

	event.Room.Sid = rInfo.RoomSid
	event.Room.Metadata = rInfo.Metadata
	event.Room.MaxParticipants = uint32(rInfo.MaxParticipants)
	event.Room.EmptyTimeout = uint32(rInfo.EmptyTimeout)

	_, err = m.ds.IncrementOrDecrementNumParticipants(rInfo.RoomSid, "-")
	if err != nil {
		log.WithError(err).Errorln("error decrementing num participants")
	}

	if strings.HasPrefix(event.Participant.Identity, config.IngressUserIdPrefix) || strings.HasPrefix(event.Participant.Identity, config.TTSAgentUserIdPrefix) {
		// if user was internal agent user then we'll have to do it manually
		// because that user did not use plugNmeet client interface
		log.Info("internal agent participant joined left, triggering OnAfterUserDisconnected manually")
		m.nm.OnAfterUserDisconnected(event.Room.Name, event.Participant.Identity)
	}
	// webhook notification
	m.sendToWebhookNotifier(event)

	log.Info("successfully processed participant_left webhook")

	m.ensureUserIsOffline(event, log)
}

// ensureUserIsOffline acts as a safety net. It verifies that a user who disconnected
// from the media server is also correctly marked as offline in the signaling system,
// fixing any discrepancies that might arise from race conditions.
func (m *WebhookModel) ensureUserIsOffline(event *livekit.WebhookEvent, log *logrus.Entry) {
	if strings.HasPrefix(event.Participant.Identity, config.IngressUserIdPrefix) {
		// as we already proceeded
		return
	}

	// for all other user's we'll do this
	nowUnix := uint64(time.Now().UnixMilli())
	time.Sleep(time.Second * 8)

	if status, err := m.natsService.GetRoomUserStatus(event.Room.Name, event.Participant.Identity); err == nil && status == natsservice.UserStatusOnline {
		userInfo, err := m.natsService.GetUserInfo(event.Room.Name, event.Participant.Identity)
		if err != nil {
			log.WithError(err).Errorln("failed to get user info from NATS")
			return
		}
		if userInfo == nil {
			return
		}
		if userInfo.ReconnectedAt > nowUnix {
			log.WithField("reconnectedAfter", userInfo.ReconnectedAt-nowUnix).Info("user reconnected, skipping manual disconnect")
			return
		}

		// user should be offline because it's disconnected from media server
		// but may be for some reason it wasn't triggered by Nats correctly
		log.Warnln("user status remain online, triggering OnAfterUserDisconnected manually")
		m.nm.OnAfterUserDisconnected(event.Room.Name, event.Participant.Identity)
	}
}
