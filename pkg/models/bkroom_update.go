package models

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"google.golang.org/protobuf/encoding/protojson"
)

func (m *BreakoutRoomModel) IncreaseBreakoutRoomDuration(r *plugnmeet.IncreaseBreakoutRoomDurationReq) error {
	room, err := m.fetchBreakoutRoom(r.RoomId, r.BreakoutRoomId)
	if err != nil {
		return err
	}

	// update in a room duration checker
	rd := NewRoomDurationModel(m.app, m.rs)
	newDuration, err := rd.IncreaseRoomDuration(r.BreakoutRoomId, r.Duration)
	if err != nil {
		return err
	}

	// now update nats
	room.Duration = newDuration
	marshal, err := protojson.Marshal(room)
	if err != nil {
		return err
	}

	return m.natsService.InsertOrUpdateBreakoutRoom(r.RoomId, r.BreakoutRoomId, marshal)
}
