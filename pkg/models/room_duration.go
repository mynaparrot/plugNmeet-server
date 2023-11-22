package models

import (
	"context"
	"errors"
	"fmt"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
	"reflect"
	"strings"
	"time"
)

const (
	roomWithDurationInfoKey = "pnm:roomWithDurationInfo"
)

type RoomDurationModel struct {
	rc  *redis.Client
	ctx context.Context
}

type RoomDurationInfo struct {
	Duration  uint64 `redis:"duration"`
	StartedAt uint64 `redis:"startedAt"`
}

func NewRoomDurationModel() *RoomDurationModel {
	return &RoomDurationModel{
		rc:  config.AppCnf.RDS,
		ctx: context.Background(),
	}
}

func (m *RoomDurationModel) AddRoomWithDurationInfo(roomId string, r RoomDurationInfo) error {
	_, err := m.rc.HSet(m.ctx, fmt.Sprintf("%s:%s", roomWithDurationInfoKey, roomId), r).Result()
	if err != nil {
		log.Error(err)
		return err
	}
	return nil
}

func (m *RoomDurationModel) DeleteRoomWithDuration(roomId string) error {
	_, err := m.rc.Del(m.ctx, fmt.Sprintf("%s:%s", roomWithDurationInfoKey, roomId)).Result()
	if err != nil {
		log.Error(err)
		return err
	}
	return nil
}

func (m *RoomDurationModel) IncreaseRoomDuration(roomId string, duration uint64) (uint64, error) {
	tm := &RoomDurationInfo{}
	field, ok := reflect.TypeOf(tm).Elem().FieldByName("Duration")
	if !ok {
		return 0, nil
	}
	durationField := field.Tag.Get("redis")

	info, err := m.GetRoomDurationInfo(roomId)
	if err != nil {
		return 0, err
	}

	// increase room duration
	roomService := NewRoomService()
	_, meta, err := roomService.LoadRoomWithMetadata(roomId)
	if err != nil {
		return 0, err
	}

	// check if this is a breakout room
	if meta.IsBreakoutRoom && info != nil {
		// need to check how long time left for this room
		now := uint64(time.Now().Unix())
		valid := info.StartedAt + (info.Duration * 60)
		d := ((valid - now) / 60) + duration

		// we'll need to make sure that breakout room duration isn't bigger than main room duration
		err = m.CompareDurationWithParentRoom(meta.ParentRoomId, d)
		if err != nil {
			return 0, err
		}
	}

	result, err := m.rc.HIncrBy(m.ctx, fmt.Sprintf("%s:%s", roomWithDurationInfoKey, roomId), durationField, int64(duration)).Result()
	if err != nil {
		return 0, err
	}
	d := uint64(result)

	meta.RoomFeatures.RoomDuration = &d
	_, err = roomService.UpdateRoomMetadataByStruct(roomId, meta)

	if err != nil {
		// if error then we'll fall back to set previous duration
		m.setRoomDuration(roomId, durationField, d-duration)
		return 0, err
	}

	return d, nil
}

func (m *RoomDurationModel) CompareDurationWithParentRoom(mainRoomId string, duration uint64) error {
	info, err := m.GetRoomDurationInfo(mainRoomId)
	if err != nil {
		return err
	}
	if info == nil {
		// this is indicating that the no info found
		return nil
	}

	now := uint64(time.Now().Unix())
	valid := info.StartedAt + (info.Duration * 60)
	left := (valid - now) / 60
	if left < duration {
		return errors.New("breakout room's duration can't be more than parent room's duration")
	}

	return nil
}

func (m *RoomDurationModel) setRoomDuration(roomId, durationField string, val uint64) {
	_, err := m.rc.HSet(m.ctx, fmt.Sprintf("%s:%s", roomWithDurationInfoKey, roomId), durationField, val).Result()
	if err != nil {
		log.Error(err)
	}
}

func (m *RoomDurationModel) GetRoomDurationInfo(roomId string) (*RoomDurationInfo, error) {
	val := new(RoomDurationInfo)
	err := m.rc.HGetAll(m.ctx, fmt.Sprintf("%s:%s", roomWithDurationInfoKey, roomId)).Scan(val)
	switch {
	case err == redis.Nil:
		return nil, nil
	case err != nil:
		return nil, err
	}
	return val, nil
}

func (m *RoomDurationModel) GetRoomsWithDurationMap() map[string]RoomDurationInfo {
	roomsKey, err := m.rc.Keys(m.ctx, "pnm:roomWithDurationInfo:*").Result()
	if err != nil {
		return nil
	}
	out := make(map[string]RoomDurationInfo)
	for _, key := range roomsKey {
		var val RoomDurationInfo
		err = m.rc.HGetAll(m.ctx, key).Scan(&val)
		if err != nil {
			log.Errorln(err)
			continue
		}
		rId := strings.Replace(key, roomWithDurationInfoKey+":", "", 1)
		out[rId] = val
	}

	return out
}
