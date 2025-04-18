package models

import (
	"context"
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	log "github.com/sirupsen/logrus"
	"strings"
	"time"
)

// checkOnlineUsersStatus will compare last ping result
// and take the decision to update user's status
func (m *SchedulerModel) checkOnlineUsersStatus() {
	locked := m.rs.IsSchedulerTaskLock("checkOnlineUsersStatus")
	if locked {
		// if lock then we will not perform here
		return
	}
	// now set lock
	_ = m.rs.LockSchedulerTask("checkOnlineUsersStatus", time.Minute*1)
	// clean at the end
	defer m.rs.UnlockSchedulerTask("checkOnlineUsersStatus")

	kl := m.app.JetStream.KeyValueStoreNames(context.Background())
	for s := range kl.Name() {
		if strings.HasPrefix(s, natsservice.RoomUsersBucketPrefix) {
			roomId := strings.ReplaceAll(s, natsservice.RoomUsersBucketPrefix, "")
			if users, err := m.natsService.GetOlineUsersId(roomId); err == nil && users != nil && len(users) > 0 {
				for _, u := range users {
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
					lastPing += natsservice.UserOnlineMaxPingDiff.Milliseconds()
					if time.Now().UnixMilli() > lastPing {
						log.Infoln(fmt.Sprintf("userId:%s should be offline, lastPing: %d, now: %d", u, lastPing, time.Now().UnixMilli()))
						m.changeUserStatus(roomId, u)
					}
				}
			}
		}
	}
}

func (m *SchedulerModel) changeUserStatus(roomId, userId string) {
	// this user should be offline
	_ = m.natsService.UpdateUserStatus(roomId, userId, natsservice.UserStatusOffline)

	if info, err := m.natsService.GetUserInfo(roomId, userId); err == nil && info != nil {
		// notify to the room
		m.natsService.BroadcastUserInfoToRoom(plugnmeet.NatsMsgServerToClientEvents_USER_OFFLINE, roomId, userId, info)
	}
}
