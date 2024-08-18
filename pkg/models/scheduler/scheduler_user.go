package schedulermodel

import (
	"context"
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"strings"
	"time"
)

// checkOnlineUsersStatus will compare last ping result
// and take the decision to update user's status
func (m *SchedulerModel) checkOnlineUsersStatus() {
	kl := m.app.JetStream.KeyValueStoreNames(context.Background())

	for s := range kl.Name() {
		if strings.HasPrefix(s, natsservice.RoomUsersBucket) {
			roomId := strings.ReplaceAll(s, natsservice.RoomUsersBucket+"-", "")
			if users, err := m.natsService.GetOlineUsersId(roomId); err == nil && users != nil && len(users) > 0 {
				for _, u := range users {
					lastPing := m.natsService.GetUserLastPing(u)
					if lastPing == 0 {
						// this user should be offline
						_ = m.natsService.UpdateUserStatus(roomId, u, natsservice.UserOffline)
						// notify to the room
						m.natsService.BroadcastUserInfoToRoom(plugnmeet.NatsMsgServerToClientEvents_USER_OFFLINE, roomId, u, nil)
						continue
					}

					// we'll compare
					lastPing += natsservice.UserOnlineMaxPingDiff.Milliseconds()
					if time.Now().UnixMilli() > lastPing {
						fmt.Println("user should be offline", lastPing, time.Now().UnixMilli())
						_ = m.natsService.UpdateUserStatus(roomId, u, natsservice.UserOffline)

						// notify to the room
						m.natsService.BroadcastUserInfoToRoom(plugnmeet.NatsMsgServerToClientEvents_USER_OFFLINE, roomId, u, nil)
					}
				}
			}
		}
	}
}
