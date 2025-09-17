package models

import (
	"fmt"
	"time"

	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	livekitservice "github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
)

func (m *WebhookModel) roomStarted(event *livekit.WebhookEvent) {
	if event.Room == nil {
		m.logger.Warnln(fmt.Sprintf("invalid webhook info received: %+v", event))
		return
	}

	// we'll check the room from kv
	rInfo, meta, err := m.natsService.GetRoomInfoWithMetadata(event.Room.Name)
	if err != nil {
		return
	}

	if rInfo == nil || meta == nil {
		// we did not find this room to our kv
		// we'll force to remove it
		lk := livekitservice.New(m.app, m.logger.Logger)
		_, err := lk.EndRoom(event.Room.Name)
		if err != nil {
			m.logger.WithError(err).Errorln("error ending room")
		}
		return
	}

	if rInfo.Status != natsservice.RoomStatusActive {
		err = m.natsService.UpdateRoomStatus(rInfo.RoomId, natsservice.RoomStatusActive)
		if err != nil {
			m.logger.WithError(err).Errorln("error updating room status")
			return
		}
	}

	meta.StartedAt = uint64(time.Now().UTC().Unix())
	if meta.RoomFeatures.GetRoomDuration() > 0 {
		// we'll add room info in map
		err := m.rmDuration.AddRoomWithDurationInfo(rInfo.RoomId, &RoomDurationInfo{
			Duration:  meta.RoomFeatures.GetRoomDuration(),
			StartedAt: meta.StartedAt,
		})
		if err != nil {
			m.logger.WithError(err).Errorln("error adding room duration info")
		}
	}

	if meta.IsBreakoutRoom {
		err := m.bm.PostTaskAfterRoomStartWebhook(rInfo.RoomId, meta)
		if err != nil {
			m.logger.WithError(err).Errorln("error posting task after room start webhook")
		}
	}

	err = m.natsService.UpdateAndBroadcastRoomMetadata(rInfo.RoomId, meta)
	if err != nil {
		m.logger.WithError(err).Errorln("error updating room metadata")
	}

	// for room_started event we should send webhook at the end
	// otherwise some services may not be ready
	event.Room.Metadata = rInfo.Metadata
	event.Room.Sid = rInfo.RoomSid
	event.Room.MaxParticipants = uint32(rInfo.MaxParticipants)
	event.Room.EmptyTimeout = uint32(rInfo.EmptyTimeout)

	// webhook notification
	m.sendToWebhookNotifier(event)
}

func (m *WebhookModel) roomFinished(event *livekit.WebhookEvent) {
	if event.Room == nil {
		m.logger.Warnln(fmt.Sprintf("invalid webhook info received: %+v", event))
		return
	}

	rInfo, err := m.natsService.GetRoomInfo(event.Room.Name)
	if err != nil || rInfo == nil {
		return
	}

	event.Room.Metadata = rInfo.Metadata
	event.Room.Sid = rInfo.RoomSid
	event.Room.MaxParticipants = uint32(rInfo.MaxParticipants)
	event.Room.EmptyTimeout = uint32(rInfo.EmptyTimeout)

	// we are introducing a new event name here
	// because for our case we still have remaining tasks
	m.sendCustomTypeWebhook(event, "session_ended")

	if rInfo.Status != natsservice.RoomStatusEnded {
		// so, this session was not ended by API call
		// may be for some reason room was ended by livekit

		// change status to ended
		err = m.natsService.UpdateRoomStatus(rInfo.RoomId, natsservice.RoomStatusEnded)
		if err != nil {
			m.logger.WithError(err).Errorln("error updating room status")
		}
		// end the room in proper way
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
		m.logger.WithError(err).Errorln("error deleting webhook")
	}
}
