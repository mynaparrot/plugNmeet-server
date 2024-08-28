package models

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	log "github.com/sirupsen/logrus"
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

		rInfo, err := m.natsService.GetRoomInfo(room.RoomId)
		if err != nil {
			log.Errorln(err)
			continue
		}

		// we did not find the room,
		// so, we're closing it
		if rInfo == nil {
			_, err = m.ds.UpdateRoomStatus(&dbmodels.RoomInfo{
				ID:        room.ID,
				IsRunning: 0,
				Ended:     time.Now().UTC(),
			})
			if err != nil {
				log.Errorln(err)
			}
			continue
		}

		userIds, err := m.natsService.GetOlineUsersId(room.RoomId)
		if err != nil {
			log.Errorln(err)
			continue
		}

		if userIds == nil || len(userIds) == 0 {
			// no user online
			valid := rInfo.CreatedAt + rInfo.EmptyTimeout
			if uint64(time.Now().UTC().Unix()) > valid {
				log.Infoln("EmptyTimeout for roomId:", room.RoomId, "passed: ", uint64(time.Now().UTC().Unix())-valid)

				// end room by proper channel
				m.rm.EndRoom(&plugnmeet.RoomEndReq{RoomId: room.RoomId})
				continue
			}
		}

		var count = int64(len(userIds))
		if room.JoinedParticipants != count {
			_, _ = m.ds.UpdateNumParticipants(room.Sid, count)
		}
	}
}
