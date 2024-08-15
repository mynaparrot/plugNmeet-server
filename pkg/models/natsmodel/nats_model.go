package natsmodel

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/dbservice"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/natsservice"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redisservice"
	"time"
)

type NatsModel struct {
	app         *config.AppConfig
	ds          *dbservice.DatabaseService
	rs          *redisservice.RedisService
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

	return &NatsModel{
		app:         app,
		ds:          ds,
		rs:          rs,
		natsService: natsservice.New(app),
	}
}

func (m *NatsModel) OnAfterUserJoined(roomId, userId string) error {
	// update user status to online
	err := m.natsService.UpdateUserStatus(roomId, userId, natsservice.UserOnline)
	if err != nil {
		return err
	}

	// send room info

	// send room metadata
	err = m.SendRoomMetadata(roomId, &userId)
	if err != nil {
		return err
	}

	// send this user's info

	// send this user's metadata
	err = m.SendUserMetadata(roomId, userId, &userId)
	if err != nil {
		return err
	}

	// send users' list

	return nil
}

// OnAfterUserDisconnected should be run in separate goroutine
// we'll wait for 5 seconds before declare user as offline
// but will broadcast as disconnected
func (m *NatsModel) OnAfterUserDisconnected(roomId, userId string) error {
	err := m.natsService.UpdateUserStatus(roomId, userId, natsservice.UserDisconnected)
	if err != nil {
		return err
	}

	// notify to everyone of the room &
	// 1. pause all the media but not from the list

	// we'll wait 5 seconds before declare this user as offline
	// 2. remove from the online list but not delete as user may reconnect again
	for i := 0; i < 5; i++ {
		if status, err := m.natsService.GetRoomUserStatus(roomId, userId); err == nil {
			if status == natsservice.UserOnline {
				// we'll broadcast the user as online again
				return nil
			}
		}
		time.Sleep(1 * time.Second)
	}

	err = m.natsService.UpdateUserStatus(roomId, userId, natsservice.UserOffline)
	if err != nil {
		return err
	}
	// now broadcast to everyone

	return nil
}
