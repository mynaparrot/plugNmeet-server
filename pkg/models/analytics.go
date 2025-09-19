package models

import (
	"context"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

const (
	analyticsRoomKey = redisservice.Prefix + "analytics:%s"
	analyticsUserKey = analyticsRoomKey + ":user:%s"
)

type AnalyticsModel struct {
	ctx             context.Context
	app             *config.AppConfig
	ds              *dbservice.DatabaseService
	rs              *redisservice.RedisService
	natsService     *natsservice.NatsService
	webhookNotifier *helpers.WebhookNotifier
	logger          *logrus.Entry
}

func NewAnalyticsModel(ctx context.Context, app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, natsService *natsservice.NatsService, webhookNotifier *helpers.WebhookNotifier, logger *logrus.Logger) *AnalyticsModel {
	return &AnalyticsModel{
		ctx:             ctx,
		app:             app,
		ds:              ds,
		rs:              rs,
		natsService:     natsService,
		webhookNotifier: webhookNotifier,
		logger:          logger.WithField("model", "analytics"),
	}
}
