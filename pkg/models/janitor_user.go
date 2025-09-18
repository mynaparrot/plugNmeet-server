package models

import (
	"context"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/sirupsen/logrus"
)

// checkOnlineUsersStatus will compare last ping result
// and take the decision to update user's status
func (m *JanitorModel) checkOnlineUsersStatus() {
	log := m.logger.WithField("task", "checkOnlineUsersStatus")

	locked := m.rs.IsJanitorTaskLock("checkOnlineUsersStatus")
	if locked {
		// if lock then we will not perform here
		return
	}
	// now set lock
	m.rs.LockJanitorTask("checkOnlineUsersStatus", time.Minute*1)
	// clean at the end
	defer m.rs.UnlockJanitorTask("checkOnlineUsersStatus")

	kl := m.app.JetStream.KeyValueStoreNames(context.Background())
	for s := range kl.Name() {
		if !strings.HasPrefix(s, natsservice.RoomUsersBucketPrefix) {
			continue
		}
		roomId := strings.ReplaceAll(s, natsservice.RoomUsersBucketPrefix, "")
		if users, err := m.natsService.GetOnlineUsersId(roomId); err == nil && users != nil && len(users) > 0 {
			for _, u := range users {
				userLog := log.WithFields(logrus.Fields{
					"roomId": roomId,
					"userId": u,
				})
				if strings.HasPrefix(u, config.IngressUserIdPrefix) {
					// we won't get ping from ingress user
					// so, we can't check from here.
					continue
				}
				lastPing := m.natsService.GetUserLastPing(roomId, u)
				if lastPing == 0 {
					m.changeUserStatus(roomId, u)
					continue
				}

				// we'll compare
				maxDiff := natsservice.UserOnlineMaxPingDiff.Milliseconds()
				deadline := lastPing + maxDiff
				now := time.Now().UnixMilli()
				if now > deadline {
					userLog.WithFields(logrus.Fields{
						"lastPing":     time.UnixMilli(lastPing).Format(time.RFC3339),
						"now":          time.UnixMilli(now).Format(time.RFC3339),
						"diff_ms":      now - lastPing,
						"threshold_ms": maxDiff,
						"over_by_ms":   now - deadline,
					}).Warn("user missed ping deadline, marking as offline")
					m.changeUserStatus(roomId, u)
				}
			}
		}
	}
}

func (m *JanitorModel) changeUserStatus(roomId, userId string) {
	// this user should be offline
	_ = m.natsService.UpdateUserStatus(roomId, userId, natsservice.UserStatusOffline)

	if info, err := m.natsService.GetUserInfo(roomId, userId); err == nil && info != nil {
		// notify to the room
		m.natsService.BroadcastUserInfoToRoom(plugnmeet.NatsMsgServerToClientEvents_USER_OFFLINE, roomId, userId, info)
	}
}
