package breakoutroommodel

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"google.golang.org/protobuf/encoding/protojson"
)

func (m *BreakoutRoomModel) IncreaseBreakoutRoomDuration(r *plugnmeet.IncreaseBreakoutRoomDurationReq) error {
	room, err := m.fetchBreakoutRoom(r.RoomId, r.BreakoutRoomId)
	if err != nil {
		return err
	}

	// update in room duration checker
	rd := models.NewRoomDurationModel()
	newDuration, err := rd.IncreaseRoomDuration(r.BreakoutRoomId, r.Duration)
	if err != nil {
		return err
	}

	// now update redis
	room.Duration = newDuration
	marshal, err := protojson.Marshal(room)
	if err != nil {
		return err
	}
	val := map[string]string{
		r.BreakoutRoomId: string(marshal),
	}
	err = m.rs.InsertOrUpdateBreakoutRoom(r.RoomId, val)

	return err
}
