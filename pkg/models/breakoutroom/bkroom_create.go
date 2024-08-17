package breakoutroommodel

import (
	"errors"
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/analytics"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/roomduration"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
	"time"
)

func (m *BreakoutRoomModel) CreateBreakoutRooms(r *plugnmeet.CreateBreakoutRoomsReq) error {
	mainRoom, meta, err := m.lk.LoadRoomWithMetadata(r.RoomId)
	if err != nil {
		return err
	}

	// let's check if the parent room has a duration set or not
	if meta.RoomFeatures.RoomDuration != nil && *meta.RoomFeatures.RoomDuration > 0 {
		rDuration := roomdurationmodel.New(m.app, m.rs, m.lk)
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
		bRoom.RoomId = fmt.Sprintf("%s-%s", r.RoomId, room.Id)
		meta.RoomTitle = room.Title
		bRoom.Metadata = meta
		status, msg, _ := m.rm.CreateRoom(bRoom)

		if !status {
			log.Error(msg)
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
			err = m.broadcastNotification(r.RoomId, r.RequestedUserId, u.Id, bRoom.RoomId, plugnmeet.DataMsgType_SYSTEM, plugnmeet.DataMsgBodyType_JOIN_BREAKOUT_ROOM, false)
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
	origMeta, err := m.lk.UnmarshalRoomMetadata(mainRoom.Metadata)
	if err != nil {
		return err
	}
	origMeta.RoomFeatures.BreakoutRoomFeatures.IsActive = true
	_, err = m.lk.UpdateRoomMetadataByStruct(r.RoomId, origMeta)

	// send analytics
	analyticsModel := analyticsmodel.New(m.app, m.ds, m.rs, m.lk)
	analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_BREAKOUT_ROOM,
		RoomId:    r.RoomId,
	})

	return err
}
