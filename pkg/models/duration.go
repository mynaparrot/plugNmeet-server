package models

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

type RoomDurationModel struct {
	app         *config.AppConfig
	ds          *dbservice.DatabaseService
	rs          *redisservice.RedisService
	natsService *natsservice.NatsService
	logger      *logrus.Entry
}

func NewRoomDurationModel(app *config.AppConfig, rs *redisservice.RedisService, natsService *natsservice.NatsService, logger *logrus.Logger) *RoomDurationModel {
	return &RoomDurationModel{
		app:         app,
		rs:          rs,
		natsService: natsService,
		logger:      logger.WithField("model", "room_duration"),
	}
}
