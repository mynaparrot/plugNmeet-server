package models

import (
	"context"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"github.com/sirupsen/logrus"
)

// activeRoomChecker will check & do reconciliation between DB & livekit
func (m *JanitorModel) activeRoomChecker() {
	log := m.logger.WithField("task", "activeRoomChecker")
	locked := m.rs.IsJanitorTaskLock("activeRoomChecker")
	if locked {
		// if lock then we will not perform here
		return
	}

	// now set lock
	m.rs.LockJanitorTask("activeRoomChecker", time.Minute*10)
	// clean at the end
	defer m.rs.UnlockJanitorTask("activeRoomChecker")

	activeRooms, err := m.ds.GetActiveRoomsInfo()
	if err != nil {
		return
	}

	if len(activeRooms) == 0 {
		return
	}

	for _, room := range activeRooms {
		if room.Sid == "" {
			// if room RoomSid is empty then we won't do anything
			// because may be the session is creating
			// if we don't consider this, then it will unnecessarily create empty field
			continue
		}

		rInfo, err := m.natsService.GetRoomInfo(room.RoomId)
		if err != nil {
			log.WithError(err).Errorln("error getting room info")
			continue
		}

		// we did not find the room,
		// so, we're closing it
		if rInfo == nil {
			_, err = m.ds.UpdateRoomStatus(&dbmodels.RoomInfo{
				ID:        room.ID,
				IsRunning: 0,
			})
			if err != nil {
				log.WithError(err).Errorln("error updating room status")
			}
			continue
		}

		userIds, err := m.natsService.GetOnlineUsersId(room.RoomId)
		if err != nil {
			log.WithError(err).Errorln("error getting online users")
			continue
		}

		if userIds == nil || len(userIds) == 0 {
			// no user online
			valid := rInfo.CreatedAt + rInfo.EmptyTimeout
			if uint64(time.Now().UTC().Unix()) > valid {
				log.WithFields(logrus.Fields{
					"emptyTimeout": rInfo.EmptyTimeout,
					"createdAt":    rInfo.CreatedAt,
					"validUntil":   valid,
					"secondsOver":  uint64(time.Now().UTC().Unix()) - valid,
				}).Info("closing empty room as it reached empty timeout")

				// end room by proper channel
				m.rm.EndRoom(context.Background(), &plugnmeet.RoomEndReq{RoomId: room.RoomId})
				continue
			}
		}

		var count = int64(len(userIds))
		if room.JoinedParticipants != count {
			_, _ = m.ds.UpdateNumParticipants(room.Sid, count)
		}
	}
}
