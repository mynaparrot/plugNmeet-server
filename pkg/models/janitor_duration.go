package models

import (
	"context"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

func (m *JanitorModel) checkRoomWithDuration() {
	rooms := m.rmDuration.GetRoomsWithDurationMap()
	for i, r := range rooms {
		now := uint64(time.Now().Unix())
		valid := r.StartedAt + (r.Duration * 60)
		if now > valid {
			_, _ = m.rm.EndRoom(context.Background(), &plugnmeet.RoomEndReq{
				RoomId: i,
			})
		}
	}
}
