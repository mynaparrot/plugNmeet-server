package models

import (
	"errors"
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
	"time"
)

const BreakoutRoomFormat = "%s-%s"

func (m *BreakoutRoomModel) CreateBreakoutRooms(r *plugnmeet.CreateBreakoutRoomsReq) error {
	mainRoom, meta, err := m.natsService.GetRoomInfoWithMetadata(r.RoomId)
	if err != nil {
		return err
	}

	// let's check if the parent room has a duration set or not
	if meta.RoomFeatures.RoomDuration != nil && *meta.RoomFeatures.RoomDuration > 0 {
		rDuration := NewRoomDurationModel(m.app, m.rs)
		err = rDuration.CompareDurationWithParentRoom(r.RoomId, r.Duration)
		if err != nil {
			return err
		}
	}

	// set room duration
	meta.RoomFeatures.RoomDuration = &r.Duration
	meta.IsBreakoutRoom = true
	meta.WelcomeMessage = r.WelcomeMsg
	meta.ParentRoomId = r.RoomId

	// disable few features
	meta.RoomFeatures.BreakoutRoomFeatures.IsAllow = false
	meta.RoomFeatures.WaitingRoomFeatures.IsActive = false

	// we'll disable now. in the future, we can think about those
	meta.RoomFeatures.RecordingFeatures.IsAllow = false
	meta.RoomFeatures.AllowRtmp = false

	// clear few main room data
	meta.RoomFeatures.DisplayExternalLinkFeatures.IsActive = false
	meta.RoomFeatures.ExternalMediaPlayerFeatures.IsActive = false

	e := make(map[string]bool)

	for _, room := range r.Rooms {
		bRoom := new(plugnmeet.CreateRoomReq)
		bRoom.RoomId = fmt.Sprintf(BreakoutRoomFormat, r.RoomId, room.Id)
		meta.RoomTitle = room.Title
		bRoom.Metadata = meta
		_, err := m.rm.CreateRoom(bRoom)

		if err != nil {
			log.Errorln(err)
			e[bRoom.RoomId] = true
			continue
		}

		room.Duration = r.Duration
		room.Created = uint64(time.Now().Unix())

		marshal, err := protojson.Marshal(room)
		if err != nil {
			log.Error(err)
			e[bRoom.RoomId] = true
			continue
		}

		val := map[string]string{
			bRoom.RoomId: string(marshal),
		}

		err = m.rs.InsertOrUpdateBreakoutRoom(r.RoomId, val)
		if err != nil {
			log.Error(err)
			e[bRoom.RoomId] = true
			continue
		}

		// now send invitation notification
		for _, u := range room.Users {
			err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_JOIN_BREAKOUT_ROOM, r.RoomId, bRoom.RoomId, &u.Id)
			if err != nil {
				log.Error(err)
				continue
			}
		}
	}

	if len(e) == len(r.Rooms) {
		return errors.New("breakout room creation wasn't successful")
	}

	// again here for update
	origMeta, err := m.natsService.UnmarshalRoomMetadata(mainRoom.Metadata)
	if err != nil {
		return err
	}
	origMeta.RoomFeatures.BreakoutRoomFeatures.IsActive = true
	err = m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, origMeta)

	// send analytics
	analyticsModel := NewAnalyticsModel(m.app, m.ds, m.rs)
	analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_BREAKOUT_ROOM,
		RoomId:    r.RoomId,
	})

	return err
}
