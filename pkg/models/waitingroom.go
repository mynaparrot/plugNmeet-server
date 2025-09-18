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

func NewWaitingRoomModel(app *config.AppConfig, rs *redisservice.RedisService, natsService *natsservice.NatsService, logger *logrus.Logger) *WaitingRoomModel {
	return &WaitingRoomModel{
		app:         app,
		rs:          rs,
		natsService: natsService,
		logger:      logger.WithField("model", "waiting-room"),
	}
}
