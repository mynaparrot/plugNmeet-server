package models

import (
	"github.com/livekit/protocol/livekit"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	log "github.com/sirupsen/logrus"
	"time"
)

func (m *WebhookModel) roomStarted(event *livekit.WebhookEvent) {
	if event.Room == nil {
		log.Errorln("empty roomInfo")
		return
	}

	// we'll check the room from kv
	rInfo, meta, err := m.natsService.GetRoomInfoWithMetadata(event.Room.Name)
	if err != nil {
		log.Errorln(err)
		return
	}

	if rInfo == nil || meta == nil {
		// we did not find this room to our kv
		// we'll force to remove it
		_, err := m.lk.EndRoom(event.Room.Name)
		if err != nil {
			log.Errorln(err)
		}
		return
	}

	if rInfo.Status != natsservice.RoomStatusActive {
		err = m.natsService.UpdateRoomStatus(rInfo.RoomId, natsservice.RoomStatusActive)
		if err != nil {
			log.Errorln(err)
			return
		}
	}

	meta.StartedAt = uint64(time.Now().Unix())
	if meta.RoomFeatures.GetRoomDuration() > 0 {
		// we'll add room info in map
		rmDuration := NewRoomDurationModel(m.app, m.rs)
		err := rmDuration.AddRoomWithDurationInfo(rInfo.RoomId, &RoomDurationInfo{
			Duration:  meta.RoomFeatures.GetRoomDuration(),
			StartedAt: meta.StartedAt,
		})
		if err != nil {
			log.Errorln(err)
		}
	}

	if meta.IsBreakoutRoom {
		bm := NewBreakoutRoomModel(m.app, m.ds, m.rs)
		err := bm.PostTaskAfterRoomStartWebhook(rInfo.RoomId, meta)
		if err != nil {
			log.Errorln(err)
		}
	}

	err = m.natsService.UpdateAndBroadcastRoomMetadata(rInfo.RoomId, meta)
	if err != nil {
		log.Errorln(err)
	}

	// for room_started event we should send webhook at the end
	// otherwise some services may not be ready
	m.webhookNotifier.RegisterWebhook(rInfo.RoomId, rInfo.RoomSid)
	event.Room.Metadata = rInfo.Metadata
	event.Room.Sid = rInfo.RoomSid

	// webhook notification
	go m.sendToWebhookNotifier(event)
}
