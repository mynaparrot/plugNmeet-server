package natsmodel

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/auth"
	usermodel "github.com/mynaparrot/plugnmeet-server/pkg/models/user"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

type NatsModel struct {
	app         *config.AppConfig
	ds          *dbservice.DatabaseService
	rs          *redisservice.RedisService
	authModel   *authmodel.AuthModel
	natsService *natsservice.NatsService
	userModel   *usermodel.UserModel
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
		userModel:   usermodel.New(app, ds, rs, nil),
	}
}

func (m *NatsModel) HandleFromClientToServerReq(roomId, userId string, req *plugnmeet.NatsMsgClientToServer) {
	switch req.Event {
	case plugnmeet.NatsMsgClientToServerEvents_REQ_RENEW_PNM_TOKEN:
		m.RenewPNMToken(roomId, userId, req.Msg)
	case plugnmeet.NatsMsgClientToServerEvents_REQ_INITIAL_DATA:
		m.HandleInitialData(roomId, userId)
	case plugnmeet.NatsMsgClientToServerEvents_PING:
		m.HandleClientPing(roomId, userId)
	case plugnmeet.NatsMsgClientToServerEvents_REQ_RAISE_HAND:
		m.userModel.RaisedHand(roomId, userId, req.Msg)
	case plugnmeet.NatsMsgClientToServerEvents_REQ_LOWER_HAND:
		m.userModel.LowerHand(roomId, userId)
	case plugnmeet.NatsMsgClientToServerEvents_REQ_LOWER_OTHER_USER_HAND:
		m.userModel.LowerHand(roomId, req.Msg)
	}
}
