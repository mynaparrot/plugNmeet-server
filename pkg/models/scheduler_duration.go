package models

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"time"
)

func (m *SchedulerModel) checkRoomWithDuration() {
	locked := m.rs.IsSchedulerTaskLock("checkRoomWithDuration")
	if locked {
		// if lock then we will not perform here
		return
	}

	// now set lock
	_ = m.rs.LockSchedulerTask("checkRoomWithDuration", time.Minute*1)
	// clean at the end
	defer m.rs.UnlockSchedulerTask("checkRoomWithDuration")

	rooms := m.rmDuration.GetRoomsWithDurationMap()
	for i, r := range rooms {
		now := uint64(time.Now().Unix())
		valid := r.StartedAt + (r.Duration * 60)
		if now > valid {
			_, _ = m.rm.EndRoom(&plugnmeet.RoomEndReq{
				RoomId: i,
			})
		}
	}
}
