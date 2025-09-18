package models

import (
	"strings"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"
)

func (m *NatsModel) HandleInitialData(roomId, userId string) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": roomId,
		"userId": userId,
		"method": "HandleInitialData",
	})

	// send room info
	rInfo, err := m.natsService.GetRoomInfo(roomId)
	if err != nil {
		log.WithError(err).Errorln("error getting room info")
		_ = m.natsService.NotifyErrorMsg(roomId, err.Error(), &userId)
		return
	}
	if rInfo == nil {
		log.Errorln("room information not found")
		_ = m.natsService.NotifyErrorMsg(roomId, "room information not found", &userId)
		return
	}

	// send this user's info
	userInfo, err := m.natsService.GetUserInfo(roomId, userId)
	if err != nil {
		log.WithError(err).Errorln("error getting user info")
		_ = m.natsService.NotifyErrorMsg(roomId, err.Error(), &userId)
		return
	}
	if userInfo == nil {
		log.Errorln("user info not found")
		_ = m.natsService.NotifyErrorMsg(roomId, "no user found", &userId)
		return
	}

	// send media server connection info
	token, err := m.GenerateLivekitToken(roomId, userInfo)
	if err != nil {
		log.WithError(err).Errorln("failed to generate livekit token")
		_ = m.natsService.NotifyErrorMsg(roomId, err.Error(), &userId)
		return
	}
	lkHost := strings.Replace(m.app.LivekitInfo.Host, "host.docker.internal", "localhost", 1) // without this you won't be able to connect

	initial := &plugnmeet.NatsInitialData{
		Room:      rInfo,
		LocalUser: userInfo,
		MediaServerInfo: &plugnmeet.MediaServerConnInfo{
			Url:   lkHost,
			Token: token,
		},
	}

	// send important info first
	err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_RES_INITIAL_DATA, roomId, initial, &userId)
	if err != nil {
		log.WithError(err).Warnln("error sending RES_INITIAL_DATA event")
	}
}

func (m *NatsModel) HandleSendUsersList(roomId, userId string) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": roomId,
		"userId": userId,
		"method": "HandleSendUsersList",
	})

	users, err := m.natsService.GetOnlineUsersListAsJson(roomId)
	if err != nil {
		log.WithError(err).Errorln("failed to get online users list as json")
		return
	}

	if users != nil {
		err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_RES_JOINED_USERS_LIST, roomId, users, &userId)
		if err != nil {
			log.WithError(err).Warnln("error sending RES_JOINED_USERS_LIST event")
		}
	}
}
