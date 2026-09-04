package models

import (
	"time"

	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
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
	log.Infoln("Handling room_started webhook")

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
		if _, err := m.lk.EndRoom(event.Room.Name); err != nil {
			log.WithError(err).Errorln("failed to forcefully end room in livekit")
		}
		return
	}

	// Restart detection: a paused parent room's LK room was recreated after an
	// empty-close while breakout rooms were active. In that case the status was
	// kept Active and the session start/duration state preserved, so we must not
	// reset the start clock, re-arm duration, or re-notify external webhooks.
	isRestart := rInfo.Status == natsservice.RoomStatusActive && meta.StartedAt > 0

	if rInfo.Status != natsservice.RoomStatusActive {
		log.WithField("current_status", rInfo.Status).Info("updating room status to active")
		if err := m.natsService.UpdateRoomStatus(rInfo.RoomId, natsservice.RoomStatusActive); err != nil {
			log.WithError(err).Errorln("failed to update room status")
			return
		}
	}

	if !isRestart {
		meta.StartedAt = uint64(time.Now().UTC().Unix())
		if meta.RoomFeatures.GetRoomDuration() > 0 {
			log.WithField("duration", meta.RoomFeatures.GetRoomDuration()).Info("adding room to duration checker")
			// we'll add room info in map
			err := m.rm.AddRoomWithDurationInfo(rInfo.RoomId, &RoomDurationInfo{
				Duration:  meta.RoomFeatures.GetRoomDuration(),
				StartedAt: meta.StartedAt,
			})
			if err != nil {
				log.WithError(err).Errorln("failed to add room duration info")
			}
		}
	} else {
		log.Info("room restart after pause detected — preserving original session start time and duration info")
	}

	if meta.IsBreakoutRoom {
		if err := m.bm.PostTaskAfterRoomStartWebhook(rInfo.RoomId, meta); err != nil {
			log.WithError(err).Errorln("failed to run post-start task for breakout room")
		}
	}

	if err := m.natsService.UpdateAndBroadcastRoomMetadata(rInfo.RoomId, meta); err != nil {
		log.WithError(err).Errorln("failed to update and broadcast room metadata")
	}

	// for room_started event we should send webhook at the end
	// otherwise some services may not be ready
	if !isRestart {
		event.Room.Metadata = rInfo.Metadata
		event.Room.Sid = rInfo.RoomSid
		event.Room.MaxParticipants = uint32(rInfo.MaxParticipants)
		event.Room.EmptyTimeout = uint32(rInfo.EmptyTimeout)

		// webhook notification
		m.sendToWebhookNotifier(event)
	}
	log.Info("Successfully processed room_started webhook")
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

	// Use the new helper function to get room info
	rInfo, err := m.getRoomInfoFromNatsOrRedis(event.Room.Name, log)
	if err != nil {
		log.WithError(err).Errorln("failed to get room info, skipping room_finished tasks")
		return
	}

	event.Room.Metadata = rInfo.Metadata
	event.Room.Sid = rInfo.RoomSid
	event.Room.MaxParticipants = uint32(rInfo.MaxParticipants)
	event.Room.EmptyTimeout = uint32(rInfo.EmptyTimeout)

	if rInfo.Status != natsservice.RoomStatusEnded {
		// Pause guard: a natural LiveKit empty-close of a PARENT room while its
		// breakout rooms are still active is a PAUSE, not an end. We keep the
		// room status Active, preserve all session data, and suppress the
		// external webhook notification. The parent LK room will be recreated
		// automatically when a user rejoins (handled as a restart in roomStarted).
		if meta, mErr := m.natsService.GetRoomMetadataStruct(rInfo.RoomId); mErr == nil && isParentWithActiveBreakouts(meta) {
			log.Infoln("parent room closed by LiveKit while breakout rooms are active — pausing, session data preserved")
			return
		}

		// This means the room was ended directly by LiveKit (e.g., empty timeout),
		// not through the plugNmeet API. We need to trigger our cleanup flow.
		log.Warnln("room was not ended via API, triggering plugNmeet EndRoom flow")

		// change status to ended
		if err := m.natsService.UpdateRoomStatus(rInfo.RoomId, natsservice.RoomStatusEnded); err != nil {
			log.WithError(err).Errorln("failed to update room status")
		}
		// end the room in the proper plugNmeet way
		m.rm.EndRoom(m.ctx, &plugnmeet.RoomEndReq{RoomId: rInfo.RoomId})
	}

	// at the end we'll handle event notification
	m.sendToWebhookNotifier(event)

	log.Info("Successfully processed room_finished webhook")
	// webhook data will be clean after analytics export method call e.g. PrepareToExportAnalytics
}
