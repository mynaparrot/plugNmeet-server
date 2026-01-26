package models

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

type UserModel struct {
	app            *config.AppConfig
	ds             *dbservice.DatabaseService
	rs             *redisservice.RedisService
	lk             *livekitservice.LivekitService
	natsService    *natsservice.NatsService
	analyticsModel *AnalyticsModel
	am             *AuthModel
	logger         *logrus.Entry
}

func NewUserModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, lk *livekitservice.LivekitService, natsService *natsservice.NatsService, analyticsModel *AnalyticsModel, am *AuthModel, logger *logrus.Logger) *UserModel {
	return &UserModel{
		app:            app,
		ds:             ds,
		rs:             rs,
		lk:             lk,
		natsService:    natsService,
		analyticsModel: analyticsModel,
		am:             am,
		logger:         logger.WithField("model", "user"),
	}
}
