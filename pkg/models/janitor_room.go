package models

import (
	"context"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"github.com/sirupsen/logrus"
)

func (m *JanitorModel) checkRoomWithDuration() {
	rooms := m.rm.GetRoomsWithDurationMap()
	for i, r := range rooms {
		now := uint64(time.Now().Unix())
		valid := r.StartedAt + (r.Duration * 60)
		if now > valid {
			_, _, _ = m.rm.EndRoom(context.Background(), &plugnmeet.RoomEndReq{
				RoomId: i,
			})
		}
	}
}

// activeRoomChecker will check & do reconciliation between DB & livekit
func (m *JanitorModel) activeRoomChecker() {
	log := m.logger.WithField("task", "activeRoomChecker")

	activeRooms, err := m.ds.GetActiveRoomsInfo(m.ctx)
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

		meta, mErr := m.natsService.GetRoomMetadataStruct(room.RoomId)
		if mErr != nil {
			log.WithError(mErr).Warnln("error getting room metadata")
		}

		// A parent room with active breakout rooms is occupied as long as
		// any human participant is online in the parent or one of its breakouts.
		if isParentWithActiveBreakouts(meta) {
			userIds = m.collectParentBreakoutHumanUsers(room.RoomId)
		}

		if userIds == nil || len(userIds) == 0 {
			// no user online
			valid := rInfo.CreatedAt + rInfo.EmptyTimeout
			if uint64(time.Now().UTC().Unix()) > valid {
				// Parent rooms with active breakout rooms must not be empty-closed by the janitor.
				// The same applies to breakout child rooms whose parent still has active breakouts
				// (they may legitimately sit empty under self-select).
				// But: an auto per-user AI session room is the exception: its breakout rooms are created per participant,
				// so an unused pre-created room must not keep the empty main room open.
				if isParentWithActiveBreakouts(meta) && !isPerUserAISessionParent(meta) {
					log.Info("skipping empty-close of parent room with active breakout rooms — pausing, session data preserved")
					continue
				}
				if breakoutChildOfActiveParent(m.natsService, meta) {
					log.Info("skipping empty-close of breakout child room with active parent breakouts")
					continue
				}

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

// collectParentBreakoutHumanUsers returns the deduplicated online human users
// of a parent room and its active breakout rooms. Internal agent bots are
// excluded so they never inflate the participant count.
func (m *JanitorModel) collectParentBreakoutHumanUsers(parentRoomId string) []string {
	seen := make(map[string]struct{})
	var userIds []string

	addRoom := func(roomId string) {
		users, err := m.natsService.GetOnlineUsersId(roomId)
		if err != nil {
			return
		}
		for _, userId := range users {
			if config.IsUserIdInternal(userId) {
				continue
			}
			if _, ok := seen[userId]; ok {
				continue
			}
			seen[userId] = struct{}{}
			userIds = append(userIds, userId)
		}
	}

	addRoom(parentRoomId)
	if roomIds, err := m.rs.GetBreakoutRoomIdsByParentRoomId(parentRoomId); err == nil {
		for _, roomId := range roomIds {
			addRoom(roomId)
		}
	}
	return userIds
}
