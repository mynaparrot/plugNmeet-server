package models

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

type AnalyticsModel struct {
	app         *config.AppConfig
	ds          *dbservice.DatabaseService
	rs          *redisservice.RedisService
	natsService *natsservice.NatsService
	logger      *logrus.Entry
}

func NewAnalyticsModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, natsService *natsservice.NatsService, logger *logrus.Logger) *AnalyticsModel {
	return &AnalyticsModel{
		app:         app,
		ds:          ds,
		rs:          rs,
		natsService: natsService,
		logger:      logger.WithField("model", "analytics"),
	}
}
