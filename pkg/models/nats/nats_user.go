package natsmodel

import (
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	log "github.com/sirupsen/logrus"
	"time"
)

func (m *NatsModel) OnAfterUserJoined(roomId, userId string) {
	status, err := m.natsService.GetRoomUserStatus(roomId, userId)
	if err != nil {
		return
	}
	if status == natsservice.UserStatusOnline {
		// no need
		return
	}

	err = m.natsService.UpdateUserStatus(roomId, userId, natsservice.UserStatusOnline)
	if err != nil {
		log.Warnln(err)
	}

	if userInfo, err := m.natsService.GetUserInfo(roomId, userId); err == nil && userInfo != nil {
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
	err := m.natsService.UpdateUserStatus(roomId, userId, natsservice.UserStatusDisconnected)
	if err != nil {
		log.Warnln(err)
	}

	// notify to everyone of the room &
	// 1. pause all the media but not from the list
	userInfo, err := m.natsService.GetUserInfo(roomId, userId)
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

	// we'll wait 5 seconds before declare this user as offline
	// 2. remove from the online list but not delete as user may reconnect again
	time.Sleep(5 * time.Second)

	if status, err := m.natsService.GetRoomUserStatus(roomId, userId); err == nil {
		if status == natsservice.UserStatusOnline {
			// we do not need to do anything
			return
		}
	}
	err = m.natsService.UpdateUserStatus(roomId, userId, natsservice.UserStatusOffline)
	if err != nil {
		log.Warnln(err)
	}

	// now broadcast to everyone
	_ = m.natsService.BroadcastSystemEventToEveryoneExceptUserId(plugnmeet.NatsMsgServerToClientEvents_USER_OFFLINE, roomId, userInfo, userId)

	// we'll wait another 30 seconds & delete this consumer,
	// but we'll keep user's information in the bucket
	// everything will be clean when the session ends.
	time.Sleep(30 * time.Second)
	if status, err := m.natsService.GetRoomUserStatus(roomId, userId); err == nil {
		if status == natsservice.UserStatusOnline {
			// we do not need to do anything
			return
		}
	}

	// do not need to delete the user as user may come to online again
	// when the session is ended, we'll do proper clean up.
	// delete consumer only
	m.natsService.DeleteConsumer(roomId, userId)
}

func (m *NatsModel) onAfterUserLoggedOut(roomId, userId string) {
	// delete consumer
	m.natsService.DeleteConsumer(roomId, userId)
	// now delete from the room users list & bucket
	m.natsService.DeleteUser(roomId, userId)
}
