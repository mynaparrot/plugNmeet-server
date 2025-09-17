package models

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

type WaitingRoomModel struct {
	app         *config.AppConfig
	rs          *redisservice.RedisService
	natsService *natsservice.NatsService
	logger      *logrus.Entry
}

func NewWaitingRoomModel(app *config.AppConfig, rs *redisservice.RedisService, logger *logrus.Logger) *WaitingRoomModel {
	if app == nil {
		app = config.GetConfig()
	}
	if rs == nil {
		rs = redisservice.New(app.RDS, logger)
	}

	return &WaitingRoomModel{
		app:         app,
		rs:          rs,
		natsService: natsservice.New(app, logger),
		logger:      logger.WithField("model", "waiting-room"),
	}
}
