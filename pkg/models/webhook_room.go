package models

import (
	"time"

	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/sirupsen/logrus"
)

func (m *WebhookModel) roomStarted(event *livekit.WebhookEvent) {
	if event.Room == nil {
		m.logger.Warnln("received room_started webhook with nil room info")
		return
	}

	log := m.logger.WithFields(logrus.Fields{
		"roomId": event.Room.Name,
		"event":  event.GetEvent(),
	})
	log.Infoln("handling room_started webhook")

	// we'll check the room from kv
	rInfo, meta, err := m.natsService.GetRoomInfoWithMetadata(event.Room.Name)
	if err != nil {
		log.WithError(err).Errorln("failed to get room info from NATS")
		return
	}

	if rInfo == nil || meta == nil {
		// This can happen if a room is created directly in LiveKit without going through plugNmeet's API.
		// We'll forcefully end it to maintain consistency.
		log.Warnln("room not found in plugNmeet's NATS store, forcing room termination")
		_, err := m.lk.EndRoom(event.Room.Name)
		if err != nil {
			log.WithError(err).Errorln("failed to forcefully end room in livekit")
		}
		return
	}

	if rInfo.Status != natsservice.RoomStatusActive {
		log.WithField("current_status", rInfo.Status).Info("updating room status to active")
		err = m.natsService.UpdateRoomStatus(rInfo.RoomId, natsservice.RoomStatusActive)
		if err != nil {
			log.WithError(err).Errorln("failed to update room status")
			return
		}
	}

	meta.StartedAt = uint64(time.Now().UTC().Unix())
	if meta.RoomFeatures.GetRoomDuration() > 0 {
		log.WithField("duration", meta.RoomFeatures.GetRoomDuration()).Info("adding room to duration checker")
		// we'll add room info in map
		err := m.rmDuration.AddRoomWithDurationInfo(rInfo.RoomId, &RoomDurationInfo{
			Duration:  meta.RoomFeatures.GetRoomDuration(),
			StartedAt: meta.StartedAt,
		})
		if err != nil {
			log.WithError(err).Errorln("failed to add room duration info")
		}
	}

	if meta.IsBreakoutRoom {
		err := m.bm.PostTaskAfterRoomStartWebhook(rInfo.RoomId, meta)
		if err != nil {
			log.WithError(err).Errorln("failed to run post-start task for breakout room")
		}
	}

	err = m.natsService.UpdateAndBroadcastRoomMetadata(rInfo.RoomId, meta)
	if err != nil {
		log.WithError(err).Errorln("failed to update and broadcast room metadata")
	}

	// for room_started event we should send webhook at the end
	// otherwise some services may not be ready
	event.Room.Metadata = rInfo.Metadata
	event.Room.Sid = rInfo.RoomSid
	event.Room.MaxParticipants = uint32(rInfo.MaxParticipants)
	event.Room.EmptyTimeout = uint32(rInfo.EmptyTimeout)

	// webhook notification
	m.sendToWebhookNotifier(event)
	log.Info("successfully processed room_started webhook")
}

func (m *WebhookModel) roomFinished(event *livekit.WebhookEvent) {
	if event.Room == nil {
		m.logger.Warnln("received room_finished webhook with nil room info")
		return
	}

	log := m.logger.WithFields(logrus.Fields{
		"roomId": event.Room.Name,
		"event":  event.GetEvent(),
	})
	log.Infoln("handling room_finished webhook")

	rInfo, err := m.natsService.GetRoomInfo(event.Room.Name)
	if err != nil || rInfo == nil {
		if err != nil {
			log.WithError(err).Errorln("failed to get room info from NATS due to an error, falling back to redis")
		}
		// fallback to redis to retrieve room info
		rInfo = m.rs.GetTemporaryRoomData(event.Room.Name)
		if rInfo == nil {
			log.Warnln("room not found in Nats or Redis, skipping room_finished tasks")
			return
		}
	}

	event.Room.Metadata = rInfo.Metadata
	event.Room.Sid = rInfo.RoomSid
	event.Room.MaxParticipants = uint32(rInfo.MaxParticipants)
	event.Room.EmptyTimeout = uint32(rInfo.EmptyTimeout)

	// we are introducing a new event name here
	// because for our case we still have remaining tasks
	m.sendCustomTypeWebhook(event, "session_ended")

	if rInfo.Status != natsservice.RoomStatusEnded {
		// This means the room was ended directly by LiveKit (e.g., empty timeout),
		// not through the plugNmeet API. We need to trigger our cleanup flow.
		log.Warnln("room was not ended via API, triggering plugNmeet EndRoom flow")

		// change status to ended
		err = m.natsService.UpdateRoomStatus(rInfo.RoomId, natsservice.RoomStatusEnded)
		if err != nil {
			log.WithError(err).Errorln("failed to update room status to ended")
		}
		// end the room in the proper plugNmeet way
		m.rm.EndRoom(m.ctx, &plugnmeet.RoomEndReq{RoomId: rInfo.RoomId})
	}

	// now we'll perform a few service related tasks
	time.Sleep(config.WaitBeforeTriggerOnAfterRoomEnded)

	// at the end we'll handle event notification
	// send it first
	m.sendToWebhookNotifier(event)

	// now clean up webhook for this room
	err = m.webhookNotifier.DeleteWebhook(rInfo.RoomId)
	if err != nil {
		log.WithError(err).Errorln("failed to delete webhook registration")
	}
	log.Info("successfully processed room_finished webhook")
}
