package models

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

type IngressModel struct {
	app            *config.AppConfig
	ds             *dbservice.DatabaseService
	rs             *redisservice.RedisService
	lk             *livekitservice.LivekitService
	natsService    *natsservice.NatsService
	analyticsModel *AnalyticsModel
	logger         *logrus.Entry
}

func NewIngressModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, lk *livekitservice.LivekitService, natsService *natsservice.NatsService, analyticsModel *AnalyticsModel, logger *logrus.Logger) *IngressModel {
	return &IngressModel{
		app:            app,
		ds:             ds,
		rs:             rs,
		lk:             lk,
		natsService:    natsService,
		analyticsModel: analyticsModel,
		logger:         logger.WithField("model", "ingress"),
	}
}
