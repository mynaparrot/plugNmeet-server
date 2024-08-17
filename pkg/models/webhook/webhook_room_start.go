package webhookmodel

import (
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/breakoutroom"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/roomduration"
	log "github.com/sirupsen/logrus"
	"time"
)

func (m *WebhookModel) roomStarted(event *livekit.WebhookEvent) {
	if event.Room == nil {
		log.Errorln("empty roomInfo")
		return
	}

	// as livekit sent webhook instantly but our jobs may be in progress
	// we'll check if this room is still under progress or not
	m.rm.CheckAndWaitUntilRoomCreationInProgress(event.Room.GetName())

	rm, _ := m.ds.GetRoomInfoByRoomId(event.Room.GetName(), 1)
	if rm == nil || rm.ID == 0 {
		if m.app.Client.Debug {
			// then we can allow creating room
			// we'll only create if not exist
			room := &dbmodels.RoomInfo{
				RoomId:       event.Room.GetName(),
				Sid:          event.Room.GetSid(),
				IsRunning:    1,
				CreationTime: event.Room.GetCreationTime(),
				Created:      time.Now().UTC(),
			}
			_, err := m.ds.InsertOrUpdateRoomInfo(room)
			if err != nil {
				log.Errorln(err)
				return
			}
		} else {
			// in production, we should not allow processing further
			// because may be the room was created in livekit
			// but our DB was not updated because of error
			return
		}
	}

	// may be during room creation sid was not added
	// we'll check and update during production mood
	if !m.app.Client.Debug {
		if rm.Sid == "" {
			rm.Sid = event.Room.GetSid()
			// just to update
			rm.CreationTime = event.Room.GetCreationTime()
			rm.Created = time.Now().UTC()

			_, err := m.ds.InsertOrUpdateRoomInfo(rm)
			if err != nil {
				log.Errorln(err)
				return
			}
		}
	}

	// now we'll insert this session in the active sessions list
	_, err := m.rs.ManageActiveRoomsWithMetadata(event.Room.Name, "add", event.Room.Metadata)
	if err != nil {
		log.Errorln(err)
	}

	if event.Room.GetMetadata() != "" {
		info, err := m.lk.UnmarshalRoomMetadata(event.Room.Metadata)
		if err == nil {
			info.StartedAt = uint64(time.Now().Unix())
			if info.RoomFeatures.GetRoomDuration() > 0 {
				// we'll add room info in map
				rmDuration := roomdurationmodel.New(m.app, m.rs, m.lk)
				err := rmDuration.AddRoomWithDurationInfo(event.Room.Name, &roomdurationmodel.RoomDurationInfo{
					Duration:  info.RoomFeatures.GetRoomDuration(),
					StartedAt: info.GetStartedAt(),
				})
				if err != nil {
					log.Errorln(err)
				}
			}
			if info.IsBreakoutRoom {
				bm := breakoutroommodel.New(m.app, m.ds, m.rs, m.lk)
				err := bm.PostTaskAfterRoomStartWebhook(event.Room.Name, info)
				if err != nil {
					log.Errorln(err)
				}
			}
			lk, err := m.lk.UpdateRoomMetadataByStruct(event.Room.Name, info)
			if err != nil {
				log.Errorln(err)
			}
			if lk.GetMetadata() != "" {
				// use updated metadata
				event.Room.Metadata = lk.GetMetadata()
			}
		}
	}

	// for room_started event we should send webhook at the end
	// otherwise some of the services may not be ready
	m.webhookNotifier.RegisterWebhook(event.Room.GetName(), event.Room.GetSid())
	// webhook notification
	go m.sendToWebhookNotifier(event)
}
