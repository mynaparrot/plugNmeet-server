package natsmodel

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/authmodel"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/dbservice"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/natsservice"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redisservice"
	log "github.com/sirupsen/logrus"
	"time"
)

type NatsModel struct {
	app         *config.AppConfig
	ds          *dbservice.DatabaseService
	rs          *redisservice.RedisService
	authModel   *authmodel.AuthModel
	natsService *natsservice.NatsService
}

func New(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService) *NatsModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.New(app.ORM)
	}
	if rs == nil {
		rs = redisservice.New(app.RDS)
	}
	natsService := natsservice.New(app)

	return &NatsModel{
		app:         app,
		ds:          ds,
		rs:          rs,
		authModel:   authmodel.New(app, natsService),
		natsService: natsService,
	}
}

func (m *NatsModel) HandleFromClientToServerReq(roomId, userId *string, req *plugnmeet.NatsMsgClientToServer) error {
	switch req.Event {
	case plugnmeet.NatsMsgClientToServerEvents_RENEW_PNM_TOKEN:
		return m.RenewPNMToken(*roomId, *userId, req.Msg)
	}

	return nil
}

func (m *NatsModel) OnAfterUserJoined(roomId, userId string) {
	// update user status to online
	err := m.natsService.UpdateUserStatus(roomId, userId, natsservice.UserOnline)
	if err != nil {
		log.Warnln(err)
	}

	// send room info
	rInfo, err := m.natsService.GetRoomInfo(roomId)
	if err != nil {
		log.Errorln(err)
		// send an error message
		_ = m.natsService.SendSystemNotificationToUser(roomId, userId, err.Error(), plugnmeet.NatsSystemNotificationTypes_NATS_SYSTEM_NOTIFICATION_ERROR)
		return
	}

	// send this user's info
	userInfo, err := m.natsService.GetUserInfo(userId)
	if err != nil {
		log.Errorln(err)
		// send an error message
		_ = m.natsService.SendSystemNotificationToUser(roomId, userId, err.Error(), plugnmeet.NatsSystemNotificationTypes_NATS_SYSTEM_NOTIFICATION_ERROR)
		return
	}

	// send media server connection info
	token, err := m.GenerateLivekitToken(roomId, userInfo)
	if err != nil {
		log.Errorln(err)
		// send an error message
		_ = m.natsService.SendSystemNotificationToUser(roomId, userId, err.Error(), plugnmeet.NatsSystemNotificationTypes_NATS_SYSTEM_NOTIFICATION_ERROR)
		return
	}

	initial := &plugnmeet.NatsInitialData{
		Room:      rInfo,
		LocalUser: userInfo,
		MediaServerInfo: &plugnmeet.MediaServerConnInfo{
			Url:         m.app.LivekitInfo.Host,
			Token:       token,
			EnabledE2Ee: rInfo.EnabledE2Ee,
		},
	}

	// send users' list
	if users, err := m.natsService.GetOnlineUsersList(roomId); err == nil && users != nil || len(users) > 0 {
		initial.OnlineUsers = users
	}

	err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_INITIAL_DATA, roomId, initial, &userId)
	if err != nil {
		log.Warnln(err)
	}

	// broadcast this user to everyone
	err = m.natsService.BroadcastSystemEventToEveryoneExceptUserId(plugnmeet.NatsMsgServerToClientEvents_USER_JOINED, roomId, userInfo, userId)
	if err != nil {
		log.Warnln(err)
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
	err = m.natsService.BroadcastSystemEventToEveryoneExceptUserId(plugnmeet.NatsMsgServerToClientEvents_USER_DISCONNECTED, roomId, userInfo, userId)
	if err != nil {
		log.Warnln(err)
	}

	// we'll wait 5 seconds before declare this user as offline
	// 2. remove from the online list but not delete as user may reconnect again
	for i := 0; i < 5; i++ {
		if status, err := m.natsService.GetRoomUserStatus(roomId, userId); err == nil {
			if status == natsservice.UserOnline {
				// we do not need to do anything
				return
			}
		}
		time.Sleep(1 * time.Second)
	}

	err = m.natsService.UpdateUserStatus(roomId, userId, natsservice.UserOffline)
	if err != nil {
		log.Warnln(err)
	}

	// now broadcast to everyone
	_ = m.natsService.BroadcastSystemEventToEveryoneExceptUserId(plugnmeet.NatsMsgServerToClientEvents_USER_OFFLINE, roomId, userInfo, userId)

	// this user may join again & join hook will perform everything
	// we do not need to clean this user from the bucket
}
