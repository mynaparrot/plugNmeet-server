package models

import (
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	log "github.com/sirupsen/logrus"
	"time"
)

func (m *WebhookModel) roomFinished(event *livekit.WebhookEvent) {
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

	event.Room.Metadata = rInfo.Metadata
	event.Room.Sid = rInfo.RoomSid

	go func() {
		// we are introducing a new event name here
		// because for our case we still have remaining tasks
		m.sendCustomTypeWebhook(event, "session_ended")
	}()

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
	err = m.webhookNotifier.DeleteWebhook(rInfo.RoomSid)
	if err != nil {
		log.Errorln(err)
	}
}
