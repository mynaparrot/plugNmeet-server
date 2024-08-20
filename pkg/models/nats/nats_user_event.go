package natsmodel

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	log "github.com/sirupsen/logrus"
	"strings"
)

func (m *NatsModel) HandleInitialData(roomId, userId string) {
	// send room info
	rInfo, err := m.natsService.GetRoomInfo(roomId)
	if err != nil {
		log.Errorln(err)
		_ = m.natsService.NotifyErrorMsg(roomId, err.Error(), &userId)
		return
	}
	if rInfo == nil {
		_ = m.natsService.NotifyErrorMsg(roomId, "room information not found", &userId)
	}

	// send this user's info
	userInfo, err := m.natsService.GetUserInfo(roomId, userId)
	if err != nil {
		log.Errorln(err)
		_ = m.natsService.NotifyErrorMsg(roomId, err.Error(), &userId)
		return
	}
	if userInfo == nil {
		_ = m.natsService.NotifyErrorMsg(roomId, "no user found", &userId)
		return
	}

	// send media server connection info
	token, err := m.GenerateLivekitToken(roomId, userInfo)
	if err != nil {
		log.Errorln(err)
		_ = m.natsService.NotifyErrorMsg(roomId, err.Error(), &userId)
		return
	}
	lkHost := strings.Replace(m.app.LivekitInfo.Host, "host.docker.internal", "localhost", 1) // without this you won't be able to connect

	initial := &plugnmeet.NatsInitialData{
		Room:      rInfo,
		LocalUser: userInfo,
		MediaServerInfo: &plugnmeet.MediaServerConnInfo{
			Url:         lkHost,
			Token:       token,
			EnabledE2Ee: rInfo.EnabledE2Ee,
		},
	}

	// send important info first
	err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_INITIAL_DATA, roomId, initial, &userId)
	if err != nil {
		log.Warnln(err)
	}

	// now send users' list
	if users, err := m.natsService.GetOnlineUsersListAsJson(roomId); err == nil && users != nil {
		err := m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_JOINED_USERS_LIST, roomId, users, &userId)
		if err != nil {
			log.Warnln(err)
		}
	}
}
