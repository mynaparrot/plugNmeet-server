package models

import (
	"context"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

func (m *JanitorModel) checkRoomWithDuration() {
	locked := m.rs.IsJanitorTaskLock("checkRoomWithDuration")
	if locked {
		// if lock then we will not perform here
		return
	}

	// now set lock
	m.rs.LockJanitorTask("checkRoomWithDuration", time.Minute*1)
	// clean at the end
	defer m.rs.UnlockJanitorTask("checkRoomWithDuration")

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
