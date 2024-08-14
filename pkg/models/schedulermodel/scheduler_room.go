package schedulermodel

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"time"
)

// activeRoomChecker will check & do reconciliation between DB & livekit
func (m *SchedulerModel) activeRoomChecker() {
	locked, _ := m.rs.ManageSchedulerLock("exist", "activeRoomChecker", 0)
	if locked {
		// if lock then we will not perform here
		return
	}

	// now set lock
	_, _ = m.rs.ManageSchedulerLock("add", "activeRoomChecker", time.Minute*10)
	// clean at the end
	defer m.rs.ManageSchedulerLock("del", "activeRoomChecker", 0)

	activeRooms, err := m.ds.GetActiveRoomsInfo()
	if err != nil {
		return
	}

	if len(activeRooms) == 0 {
		return
	}

	for _, room := range activeRooms {
		if room.Sid == "" {
			// if room Sid is empty then we won't do anything
			// because may be the session is creating
			// if we don't consider this, then it will unnecessarily create empty field
			continue
		}

		lkRoom, err := m.lk.LoadRoomInfo(room.RoomId)
		if lkRoom == nil && err.Error() == config.RequestedRoomNotExist {
			_, _ = m.ds.UpdateRoomStatus(&dbmodels.RoomInfo{
				Sid:       room.Sid,
				IsRunning: 0,
				Ended:     time.Now().UTC(),
			})
			continue
		} else if lkRoom == nil {
			continue
		}

		pp, err := m.lk.LoadParticipants(room.RoomId)
		if err != nil {
			continue
		}
		var count int64 = 0
		for _, p := range pp {
			if p.Identity == config.RecorderBot || p.Identity == config.RtmpBot {
				continue
			}
			count++
		}
		if room.JoinedParticipants != count {
			_, _ = m.ds.UpdateNumParticipants(room.Sid, count)
		} else if room.JoinedParticipants == 0 {
			// this room doesn't have any user
			// we'll check if room was created long before then we can end it
			// here we can check if room was created more than 24 hours ago
			expire := time.Unix(room.CreationTime, 0).Add(time.Hour * 24)
			if time.Now().UTC().After(expire) {
				// we can close the room
				_, _ = m.lk.EndRoom(room.RoomId)
			}
		}
	}
}
