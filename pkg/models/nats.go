package models

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	livekitservice "github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
)

type NatsModel struct {
	app            *config.AppConfig
	ds             *dbservice.DatabaseService
	rs             *redisservice.RedisService
	lk             *livekitservice.LivekitService
	authModel      *AuthModel
	natsService    *natsservice.NatsService
	userModel      *UserModel
	analyticsModel *AnalyticsModel
	logger         *logrus.Entry
}

func NewNatsModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, natsService *natsservice.NatsService, lk *livekitservice.LivekitService, analyticsModel *AnalyticsModel, authModel *AuthModel, userModel *UserModel, logger *logrus.Logger) *NatsModel {
	return &NatsModel{
		app:            app,
		ds:             ds,
		rs:             rs,
		lk:             lk,
		natsService:    natsService,
		authModel:      authModel,
		userModel:      userModel,
		analyticsModel: analyticsModel,
		logger:         logger.WithField("model", "nats"),
	}
}

func (m *NatsModel) HandleFromClientToServerReq(roomId, userId string, req *plugnmeet.NatsMsgClientToServer) {
	switch req.Event {
	case plugnmeet.NatsMsgClientToServerEvents_REQ_RENEW_PNM_TOKEN:
		m.RenewPNMToken(roomId, userId, req.Msg)
	case plugnmeet.NatsMsgClientToServerEvents_REQ_INITIAL_DATA:
		m.HandleInitialData(roomId, userId)
	case plugnmeet.NatsMsgClientToServerEvents_REQ_JOINED_USERS_LIST:
		m.HandleSendUsersList(roomId, userId, nil)
	case plugnmeet.NatsMsgClientToServerEvents_REQ_MEDIA_SERVER_DATA:
		m.HandleMediaServerInfo(roomId, userId, true)
	case plugnmeet.NatsMsgClientToServerEvents_PING:
		m.HandleClientPing(roomId, userId)
	case plugnmeet.NatsMsgClientToServerEvents_REQ_RAISE_HAND:
		m.userModel.RaisedHand(roomId, userId, req.Msg)
	case plugnmeet.NatsMsgClientToServerEvents_REQ_LOWER_HAND:
		m.userModel.LowerHand(roomId, userId)
	case plugnmeet.NatsMsgClientToServerEvents_REQ_LOWER_OTHER_USER_HAND:
		m.userModel.LowerHand(roomId, req.Msg)
	case plugnmeet.NatsMsgClientToServerEvents_PUSH_ANALYTICS_DATA:
		ad := new(plugnmeet.AnalyticsDataMsg)
		err := protojson.Unmarshal([]byte(req.Msg), ad)
		if err != nil {
			m.logger.Errorln(err)
			return
		}
		m.analyticsModel.HandleEvent(ad)
	case plugnmeet.NatsMsgClientToServerEvents_REQ_ONLINE_USERS_LIST:
		event := plugnmeet.NatsMsgServerToClientEvents_RESP_ONLINE_USERS_LIST
		m.HandleSendUsersList(roomId, userId, &event)
	case plugnmeet.NatsMsgClientToServerEvents_REQ_PRIVATE_DATA_DELIVERY:
		m.HandleToDeliveryPrivateData(roomId, userId, req)
	}
}
