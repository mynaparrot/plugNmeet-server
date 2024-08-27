package schedulermodel

import (
	log "github.com/sirupsen/logrus"
	"time"
)

func (m *SchedulerModel) checkRoomWithDuration() {
	locked, _ := m.rs.ManageSchedulerLock("exist", "checkRoomWithDuration", 0)
	if locked {
		// if lock then we will not perform here
		return
	}

	// now set lock
	_, _ = m.rs.ManageSchedulerLock("add", "checkRoomWithDuration", time.Minute*4)
	// clean at the end
	defer m.rs.ManageSchedulerLock("del", "checkRoomWithDuration", 0)

	rooms := m.rmDuration.GetRoomsWithDurationMap()
	for i, r := range rooms {
		now := uint64(time.Now().Unix())
		valid := r.StartedAt + (r.Duration * 60)
		if now > valid {
			_, err := m.lk.EndRoom(i)
			if err != nil {
				log.Errorln(err)
			}
		}
	}
}
