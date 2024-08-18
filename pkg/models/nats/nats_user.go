package natsmodel

import (
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	log "github.com/sirupsen/logrus"
	"time"
)

func (m *NatsModel) OnAfterUserJoined(roomId, userId string) {
	// now update status back to online
	err := m.natsService.UpdateUserStatus(roomId, userId, natsservice.UserOnline)
	if err != nil {
		log.Warnln(err)
	}

	// get old value first
	if userInfo, err := m.natsService.GetUserInfo(userId); err == nil && userInfo != nil {
		// broadcast this user to everyone
		err := m.natsService.BroadcastSystemEventToEveryoneExceptUserId(plugnmeet.NatsMsgServerToClientEvents_USER_JOINED, roomId, userInfo, userId)
		if err != nil {
			log.Warnln(err)
		}
	}
}

// OnAfterUserDisconnected should be run in separate goroutine
// we'll wait for 5 seconds before declare user as offline
// but will broadcast as disconnected
func (m *NatsModel) OnAfterUserDisconnected(roomId, userId string) {
	// TODO: need to check if the session was ended or not
	// if ended, then we do not need to do anything else.

	// now change the user's status
	err := m.natsService.UpdateUserStatus(roomId, userId, natsservice.UserDisconnected)
	if err != nil {
		log.Warnln(err)
	}

	// notify to everyone of the room &
	// 1. pause all the media but not from the list
	userInfo, err := m.natsService.GetUserInfo(userId)
	if err != nil {
		log.Warnln(err)
	}
	if userInfo == nil {
		log.Warnln(fmt.Sprintf("no user info found with id: %s, which should not happen", userId))
		// no way to continue
		// but this should not happen
		return
	}
	_ = m.natsService.BroadcastSystemEventToEveryoneExceptUserId(plugnmeet.NatsMsgServerToClientEvents_USER_DISCONNECTED, roomId, userInfo, userId)

	// we'll wait 10 seconds before declare this user as offline
	// 2. remove from the online list but not delete as user may reconnect again
	for i := 0; i < 10; i++ {
		time.Sleep(1 * time.Second)
		if status, err := m.natsService.GetRoomUserStatus(roomId, userId); err == nil {
			if status == natsservice.UserOnline {
				// we do not need to do anything
				return
			}
		}
	}

	err = m.natsService.UpdateUserStatus(roomId, userId, natsservice.UserOffline)
	if err != nil {
		log.Warnln(err)
	}

	// now broadcast to everyone
	_ = m.natsService.BroadcastSystemEventToEveryoneExceptUserId(plugnmeet.NatsMsgServerToClientEvents_USER_OFFLINE, roomId, userInfo, userId)

	// we'll wait another 30 seconds & delete this consumer,
	// but we'll keep user's information in the bucket
	// everything will be clean when the session ends.
	for i := 0; i < 30; i++ {
		time.Sleep(1 * time.Second)
		if status, err := m.natsService.GetRoomUserStatus(roomId, userId); err == nil {
			if status == natsservice.UserOnline {
				// we do not need to do anything
				return
			}
		}
	}

	// otherwise, clean user
	m.onAfterUserLoggedOut(roomId, userId)
}

func (m *NatsModel) onAfterUserLoggedOut(roomId, userId string) {
	// delete consumer
	m.natsService.DeleteConsumer(roomId, userId)
	// now delete from the room users list & bucket
	m.natsService.DeleteUser(roomId, userId)
}
