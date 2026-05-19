package models

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
)

type BreakoutRoomModel struct {
	rs             *redisservice.RedisService
	natsService    *natsservice.NatsService
	rm             *RoomModel
	analyticsModel *AnalyticsModel
	um             *UserModel
	logger         *logrus.Entry
}

func NewBreakoutRoomModel(rm *RoomModel) *BreakoutRoomModel {
	return &BreakoutRoomModel{
		rm:             rm,
		rs:             rm.rs,
		natsService:    rm.natsService,
		analyticsModel: rm.analyticsModel,
		um:             rm.userModel,
		logger:         rm.logger.Logger.WithField("model", "breakout_room"),
	}
}

type SendBreakoutRoomMsgReq struct {
	RoomId string
	Msg    string `json:"msg" validate:"required"`
}

func (m *BreakoutRoomModel) SendBreakoutRoomMsg(r *plugnmeet.BroadcastBreakoutRoomMsgReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"parentRoomId": r.RoomId,
		"method":       "SendBreakoutRoomMsg",
	})
	log.Infoln("New request received to send message to all breakout rooms")

	rooms, err := m.fetchBreakoutRooms(r.RoomId)
	if err != nil {
		log.WithError(err).Error("Failed to fetch breakout rooms")
		return err
	}

	if rooms == nil || len(rooms) == 0 {
		log.Info("No active breakout rooms found to send message")
		return nil
	}

	for _, rr := range rooms {
		if err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_SYSTEM_CHAT_MSG, rr.Id, r.Msg, nil); err != nil {
			log.WithError(err).WithField("breakoutRoomId", rr.Id).Error("Failed to broadcast message to breakout room")
		}
	}

	log.Info("Successfully broadcasted message to all breakout rooms")
	return nil
}

func (m *BreakoutRoomModel) IncreaseBreakoutRoomDuration(r *plugnmeet.IncreaseBreakoutRoomDurationReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"parentRoomId":   r.RoomId,
		"breakoutRoomId": r.BreakoutRoomId,
		"duration":       r.Duration,
		"method":         "IncreaseBreakoutRoomDuration",
	})
	log.Infoln("New request to increase breakout room duration received")

	room, err := m.fetchBreakoutRoom(r.RoomId, r.BreakoutRoomId)
	if err != nil {
		log.WithError(err).Error("Failed to fetch breakout room info")
		return err
	}

	// update in a room duration checker
	log.Info("Increasing duration in room duration checker")
	newDuration, err := m.rm.IncreaseRoomDuration(r.BreakoutRoomId, r.Duration)
	if err != nil {
		log.WithError(err).Error("Failed to increase room duration")
		return err
	}

	// now update nats
	log.Info("Updating breakout room info in nats")
	room.Duration = newDuration
	marshal, err := protojson.Marshal(room)
	if err != nil {
		log.WithError(err).Error("Failed to marshal breakout room data")
		return err
	}

	if err = m.rs.InsertOrUpdateBreakoutRoom(r.RoomId, r.BreakoutRoomId, marshal); err != nil {
		log.WithError(err).Error("Failed to update breakout room in nats")
		return err
	}

	log.WithField("new_duration", newDuration).Info("Successfully increased breakout room duration")
	return nil
}
