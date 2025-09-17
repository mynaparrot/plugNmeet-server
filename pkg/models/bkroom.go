package models

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

type BreakoutRoomModel struct {
	app            *config.AppConfig
	ds             *dbservice.DatabaseService
	rs             *redisservice.RedisService
	rm             *RoomModel
	rDuration      *RoomDurationModel
	analyticsModel *AnalyticsModel
	um             *UserModel
	natsService    *natsservice.NatsService
	logger         *logrus.Entry
}

func NewBreakoutRoomModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, natsService *natsservice.NatsService, rm *RoomModel, rDuration *RoomDurationModel, analyticsModel *AnalyticsModel, um *UserModel, logger *logrus.Logger) *BreakoutRoomModel {
	return &BreakoutRoomModel{
		app:            app,
		ds:             ds,
		rs:             rs,
		rm:             rm,
		um:             um,
		rDuration:      rDuration,
		natsService:    natsService,
		analyticsModel: analyticsModel,
		logger:         logger.WithField("model", "breakout_room"),
	}
}
