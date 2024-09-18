package models

import (
	"fmt"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	livekitservice "github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	log "github.com/sirupsen/logrus"
	"time"
)

func (m *WebhookModel) roomStarted(event *livekit.WebhookEvent) {
	if event.Room == nil {
		log.Warnln(fmt.Sprintf("invalid webhook info received: %+v", event))
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
		lk := livekitservice.New(m.app)
		_, err := lk.EndRoom(event.Room.Name)
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

	meta.StartedAt = uint64(time.Now().UTC().Unix())
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
	event.Room.Metadata = rInfo.Metadata
	event.Room.Sid = rInfo.RoomSid

	// webhook notification
	m.sendToWebhookNotifier(event)
}

func (m *WebhookModel) roomFinished(event *livekit.WebhookEvent) {
	if event.Room == nil {
		log.Warnln(fmt.Sprintf("invalid webhook info received: %+v", event))
		return
	}

	rInfo, err := m.natsService.GetRoomInfo(event.Room.Name)
	if err != nil || rInfo == nil {
		return
	}

	event.Room.Metadata = rInfo.Metadata
	event.Room.Sid = rInfo.RoomSid

	// we are introducing a new event name here
	// because for our case we still have remaining tasks
	go m.sendCustomTypeWebhook(event, "session_ended")

	if rInfo.Status != natsservice.RoomStatusEnded {
		// so, this session was not ended by API call
		// may be for some reason room was ended by livekit

		// change status to ended
		err = m.natsService.UpdateRoomStatus(rInfo.RoomId, natsservice.RoomStatusEnded)
		if err != nil {
			log.Errorln(err)
		}
		// end the room in proper way
		m.rm.EndRoom(&plugnmeet.RoomEndReq{RoomId: rInfo.RoomId})
	}

	// now we'll perform a few service related tasks
	time.Sleep(config.WaitBeforeTriggerOnAfterRoomEnded)

	// at the end we'll handle event notification
	// send it first
	m.sendToWebhookNotifier(event)

	// now clean up webhook for this room
	err = m.webhookNotifier.DeleteWebhook(rInfo.RoomId)
	if err != nil {
		log.Errorln(err)
	}
}
