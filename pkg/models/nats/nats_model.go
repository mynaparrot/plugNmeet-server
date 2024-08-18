package natsmodel

import (
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/auth"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
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

func (m *NatsModel) HandleFromClientToServerReq(roomId, userId string, req *plugnmeet.NatsMsgClientToServer) {
	switch req.Event {
	case plugnmeet.NatsMsgClientToServerEvents_REQ_RENEW_PNM_TOKEN:
		m.RenewPNMToken(roomId, userId, req.Msg)
	case plugnmeet.NatsMsgClientToServerEvents_REQ_INITIAL_DATA:
		m.HandleInitialData(roomId, userId)
	}
}

func (m *NatsModel) OnAfterUserJoined(roomId, userId string) {
	// update user status to online
	err := m.natsService.UpdateUserStatus(roomId, userId, natsservice.UserOnline)
	if err != nil {
		log.Warnln(err)
	}

	// send this user's info
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
	_ = m.natsService.BroadcastSystemEventToEveryoneExceptUserId(plugnmeet.NatsMsgServerToClientEvents_USER_DISCONNECTED, roomId, userInfo, userId)

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

	// we'll wait another 30 seconds & delete this consumer,
	// but we'll keep user's information in the bucket
	// everything will be clean when the session ends.
	for i := 0; i < 5; i++ {
		if status, err := m.natsService.GetRoomUserStatus(roomId, userId); err == nil {
			if status == natsservice.UserOnline {
				// we do not need to do anything
				return
			}
		}
		time.Sleep(6 * time.Second)
	}

	// deleting consumer for this user
	log.Infoln(fmt.Sprintf("cleaning consumer for userId: %s", userId))
	m.natsService.DeleteConsumer(roomId, userId)

	// make user as logged out
	err = m.natsService.UpdateUserStatus(roomId, userId, natsservice.UserLoggedOut)
	if err != nil {
		log.Warnln(err)
	}
}
