package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"google.golang.org/protobuf/encoding/protojson"
)

const BreakoutRoomFormat = "%s-%s"

func (m *BreakoutRoomModel) CreateBreakoutRooms(ctx context.Context, r *plugnmeet.CreateBreakoutRoomsReq) error {
	mainRoom, meta, err := m.natsService.GetRoomInfoWithMetadata(r.RoomId)
	if err != nil {
		return err
	}

	if mainRoom == nil || meta == nil {
		return errors.New("invalid parent room information")
	}

	// let's check if the parent room has a duration set or not
	if meta.RoomFeatures.RoomDuration != nil && *meta.RoomFeatures.RoomDuration > 0 {
		err = m.rDuration.CompareDurationWithParentRoom(r.RoomId, r.Duration)
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
		_, err := m.rm.CreateRoom(ctx, bRoom)

		if err != nil {
			m.logger.Errorln(err)
			e[bRoom.RoomId] = true
			continue
		}

		room.Duration = r.Duration
		room.Created = uint64(time.Now().Unix())

		marshal, err := protojson.Marshal(room)
		if err != nil {
			m.logger.Error(err)
			e[bRoom.RoomId] = true
			continue
		}

		err = m.natsService.InsertOrUpdateBreakoutRoom(r.RoomId, bRoom.RoomId, marshal)
		if err != nil {
			m.logger.Error(err)
			e[bRoom.RoomId] = true
			continue
		}

		// now send invitation notification
		for _, u := range room.Users {
			err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_JOIN_BREAKOUT_ROOM, r.RoomId, bRoom.RoomId, &u.Id)
			if err != nil {
				m.logger.Error(err)
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
	m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_BREAKOUT_ROOM,
		RoomId:    r.RoomId,
	})

	return err
}

func (m *BreakoutRoomModel) PostTaskAfterRoomStartWebhook(roomId string, metadata *plugnmeet.RoomMetadata) error {
	// now in livekit rooms are created almost instantly & sending webhook response
	// if this happened then we'll have to wait few seconds otherwise room info can't be found
	time.Sleep(config.WaitBeforeBreakoutRoomOnAfterRoomStart)

	room, err := m.fetchBreakoutRoom(metadata.ParentRoomId, roomId)
	if err != nil {
		return err
	}
	room.Created = metadata.StartedAt
	room.Started = true

	marshal, err := protojson.Marshal(room)
	if err != nil {
		return err
	}

	err = m.natsService.InsertOrUpdateBreakoutRoom(metadata.ParentRoomId, roomId, marshal)
	if err != nil {
		m.logger.Error(err)
		return err
	}

	return nil
}
