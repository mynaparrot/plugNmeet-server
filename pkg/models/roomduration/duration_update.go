package roomdurationmodel

import (
	"errors"
	"reflect"
	"time"
)

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
	_, meta, err := m.lk.LoadRoomWithMetadata(roomId)
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

	result, err := m.rs.UpdateRoomDuration(roomId, durationField, duration)
	if err != nil {
		return 0, err
	}
	d := uint64(result)

	meta.RoomFeatures.RoomDuration = &d
	_, err = m.lk.UpdateRoomMetadataByStruct(roomId, meta)

	if err != nil {
		// if error then we'll fall back to set previous duration
		_ = m.rs.SetRoomDuration(roomId, durationField, d-duration)
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
