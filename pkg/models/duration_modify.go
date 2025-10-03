package models

import (
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/sirupsen/logrus"
)

type RoomDurationInfo struct {
	Duration  uint64 `redis:"duration"`
	StartedAt uint64 `redis:"startedAt"`
}

func (m *RoomDurationModel) AddRoomWithDurationInfo(roomId string, r *RoomDurationInfo) error {
	log := m.logger.WithField("roomId", roomId)
	log.Info("adding room with duration info")

	err := m.rs.AddRoomWithDurationInfo(roomId, r)
	if err != nil {
		log.WithError(err).Error("failed to add room with duration info to redis")
		return err
	}

	log.Info("successfully added room with duration info")
	return nil
}

func (m *RoomDurationModel) DeleteRoomWithDuration(roomId string) error {
	log := m.logger.WithField("roomId", roomId)
	log.Info("deleting room with duration")

	err := m.rs.DeleteRoomWithDuration(roomId)
	if err != nil {
		log.WithError(err).Error("failed to delete room with duration from redis")
		return err
	}

	log.Info("successfully deleted room with duration")
	return nil
}

func (m *RoomDurationModel) IncreaseRoomDuration(roomId string, duration uint64) (uint64, error) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":   roomId,
		"duration": duration,
		"method":   "IncreaseRoomDuration",
	})
	log.Infoln("request to increase room duration")

	tm := &RoomDurationInfo{}
	field, ok := reflect.TypeOf(tm).Elem().FieldByName("Duration")
	if !ok {
		err := errors.New("duration field not found in RoomDurationInfo struct")
		log.WithError(err).Error()
		return 0, err
	}
	durationField := field.Tag.Get("redis")

	info, err := m.GetRoomDurationInfo(roomId)
	if err != nil {
		log.WithError(err).Error("failed to get room duration info")
		return 0, err
	}

	// increase room duration
	meta, err := m.natsService.GetRoomMetadataStruct(roomId)
	if err != nil {
		log.WithError(err).Error("failed to get room metadata")
		return 0, err
	}

	if meta == nil {
		err = errors.New("invalid nil room metadata information")
		log.WithError(err).Error()
		return 0, err
	}

	// check if this is a breakout room
	if meta.IsBreakoutRoom && info != nil {
		// only if the room has a duration, we will check with the parent
		if info.StartedAt == 0 {
			err = errors.New("can't increase duration as breakout room is not running")
			log.WithError(err).Warn()
			return 0, err
		}
		if info.Duration == 0 {
			err = errors.New("can't increase duration as breakout room has unlimited duration")
			log.WithError(err).Warn()
			return 0, err
		} else {
			log.Info(
				"breakout room has duration, will compare with parent room")
			// need to check how long time left for this room
			now := uint64(time.Now().Unix())
			valid := info.StartedAt + (info.Duration * 60)
			d := ((valid - now) / 60) + duration

			// we'll need to make sure that breakout room duration isn't bigger than main room duration
			err = m.CompareDurationWithParentRoom(meta.ParentRoomId, d)
			if err != nil {
				log.WithError(err).Error("duration comparison with parent room failed")
				return 0, err
			}
		}
	}

	result, err := m.rs.UpdateRoomDuration(roomId, durationField, duration)
	if err != nil {
		log.WithError(err).Error("failed to update room duration in redis")
		return 0, err
	}
	d := uint64(result)

	meta.RoomFeatures.RoomDuration = &d
	err = m.natsService.UpdateAndBroadcastRoomMetadata(roomId, meta)

	if err != nil {
		// if error then we'll fall back to set previous duration
		log.WithError(err).Error("failed to update and broadcast room metadata, rolling back redis change")
		_ = m.rs.SetRoomDuration(roomId, durationField, d-duration)
		return 0, err
	}

	log.WithField("new_duration", d).Info("successfully increased room duration")
	return d, nil
}

func (m *RoomDurationModel) CompareDurationWithParentRoom(mainRoomId string, duration uint64) error {
	log := m.logger.WithFields(logrus.Fields{
		"mainRoomId": mainRoomId,
		"duration":   duration,
		"method":     "CompareDurationWithParentRoom",
	})
	log.Info("comparing breakout room duration with parent room")

	info, err := m.GetRoomDurationInfo(mainRoomId)
	if err != nil {
		log.WithError(err).Error("failed to get parent room duration info")
		return err
	}
	if info == nil {
		// this is indicating that the no info found
		log.Info("parent room has no duration limit, comparison skipped")
		return nil
	}

	if info.Duration == 0 {
		// parent room has no duration limit
		log.Info("parent room has no duration limit, comparison skipped")
		return nil
	}

	now := uint64(time.Now().Unix())
	valid := info.StartedAt + (info.Duration * 60)
	left := (valid - now) / 60
	log.WithField("minutes_left", left).Info("parent room duration check")

	if left < duration {
		err = fmt.Errorf("breakout room's duration (%d) can't be more than parent room's remaining duration (%d)", duration, left)
		log.WithError(err).Warn()
		return err
	}

	return nil
}
