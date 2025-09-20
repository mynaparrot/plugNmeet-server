package models

import (
	"errors"
	"fmt"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
)

const BreakoutRoomFormat = "%s-%s"

func (m *BreakoutRoomModel) CreateBreakoutRooms(r *plugnmeet.CreateBreakoutRoomsReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":   r.RoomId,
		"method":   "CreateBreakoutRooms",
		"numRooms": len(r.Rooms),
	})
	log.Infoln("request to create breakout rooms")

	mainRoom, meta, err := m.natsService.GetRoomInfoWithMetadata(r.RoomId)
	if err != nil {
		log.WithError(err).Error("failed to get parent room info")
		return err
	}

	if mainRoom == nil || meta == nil {
		err = errors.New("invalid parent room information")
		log.WithError(err).Error()
		return err
	}

	// let's check if the parent room has a duration set or not
	if meta.RoomFeatures.RoomDuration != nil && *meta.RoomFeatures.RoomDuration > 0 {
		err = m.rDuration.CompareDurationWithParentRoom(r.RoomId, r.Duration)
		if err != nil {
			log.WithError(err).Error("duration comparison with parent room failed")
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
		bRoomId := fmt.Sprintf(BreakoutRoomFormat, r.RoomId, room.Id)
		roomLog := log.WithFields(logrus.Fields{
			"breakoutRoomId":    bRoomId,
			"breakoutRoomTitle": room.Title,
		})

		bRoom := new(plugnmeet.CreateRoomReq)
		bRoom.RoomId = bRoomId
		meta.RoomTitle = room.Title
		bRoom.Metadata = meta
		_, err := m.rm.CreateRoom(bRoom)

		if err != nil {
			roomLog.WithError(err).Error("failed to create breakout room")
			e[bRoom.RoomId] = true
			continue
		}

		room.Duration = r.Duration
		room.Created = uint64(time.Now().Unix())

		marshal, err := protojson.Marshal(room)
		if err != nil {
			roomLog.WithError(err).Error("failed to marshal breakout room data")
			e[bRoom.RoomId] = true
			continue
		}

		err = m.natsService.InsertOrUpdateBreakoutRoom(r.RoomId, bRoom.RoomId, marshal)
		if err != nil {
			roomLog.WithError(err).Error("failed to insert breakout room in nats")
			e[bRoom.RoomId] = true
			continue
		}

		// now send invitation notification
		for _, u := range room.Users {
			err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_JOIN_BREAKOUT_ROOM, r.RoomId, bRoom.RoomId, &u.Id)
			if err != nil {
				roomLog.WithError(err).WithField("userId", u.Id).Error("failed to send breakout room invitation")
				continue
			}
		}
	}

	if len(e) == len(r.Rooms) {
		err = errors.New("breakout room creation wasn't successful for any room")
		log.WithError(err).Error()
		return err
	}

	// again here for update
	origMeta, err := m.natsService.UnmarshalRoomMetadata(mainRoom.Metadata)
	if err != nil {
		log.WithError(err).Error("failed to unmarshal original parent room metadata")
		return err
	}
	origMeta.RoomFeatures.BreakoutRoomFeatures.IsActive = true
	err = m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, origMeta)
	if err != nil {
		log.WithError(err).Error("failed to update parent room metadata")
	}

	// send analytics
	m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_BREAKOUT_ROOM,
		RoomId:    r.RoomId,
	})

	log.Info("finished creating breakout rooms")
	return err
}

func (m *BreakoutRoomModel) PostTaskAfterRoomStartWebhook(roomId string, metadata *plugnmeet.RoomMetadata) error {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":       roomId,
		"parentRoomId": metadata.ParentRoomId,
		"method":       "PostTaskAfterRoomStartWebhook",
	})
	log.Info("handling post-start tasks for breakout room")

	// now in livekit rooms are created almost instantly & sending webhook response
	// if this happened then we'll have to wait few seconds otherwise room info can't be found
	time.Sleep(config.WaitBeforeBreakoutRoomOnAfterRoomStart)

	room, err := m.fetchBreakoutRoom(metadata.ParentRoomId, roomId)
	if err != nil {
		log.WithError(err).Error("failed to fetch breakout room info")
		return err
	}
	room.Created = metadata.StartedAt
	room.Started = true

	marshal, err := protojson.Marshal(room)
	if err != nil {
		log.WithError(err).Error("failed to marshal breakout room data")
		return err
	}

	err = m.natsService.InsertOrUpdateBreakoutRoom(metadata.ParentRoomId, roomId, marshal)
	if err != nil {
		log.WithError(err).Error("failed to update breakout room info in nats")
		return err
	}

	log.Info("successfully handled post-start tasks for breakout room")
	return nil
}
