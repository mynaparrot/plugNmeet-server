package models

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
)

func (m *BreakoutRoomModel) IncreaseBreakoutRoomDuration(r *plugnmeet.IncreaseBreakoutRoomDurationReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"parentRoomId":   r.RoomId,
		"breakoutRoomId": r.BreakoutRoomId,
		"duration":       r.Duration,
		"method":         "IncreaseBreakoutRoomDuration",
	})
	log.Infoln("request to increase breakout room duration")

	room, err := m.fetchBreakoutRoom(r.RoomId, r.BreakoutRoomId)
	if err != nil {
		log.WithError(err).Error("failed to fetch breakout room info")
		return err
	}

	// update in a room duration checker
	log.Info("increasing duration in room duration checker")
	newDuration, err := m.rDuration.IncreaseRoomDuration(r.BreakoutRoomId, r.Duration)
	if err != nil {
		log.WithError(err).Error("failed to increase room duration")
		return err
	}

	// now update nats
	log.Info("updating breakout room info in nats")
	room.Duration = newDuration
	marshal, err := protojson.Marshal(room)
	if err != nil {
		log.WithError(err).Error("failed to marshal breakout room data")
		return err
	}

	err = m.natsService.InsertOrUpdateBreakoutRoom(r.RoomId, r.BreakoutRoomId, marshal)
	if err != nil {
		log.WithError(err).Error("failed to update breakout room in nats")
		return err
	}

	log.WithField("new_duration", newDuration).Info("successfully increased breakout room duration")
	return nil
}
