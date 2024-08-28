package models

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

type PollModel struct {
	app            *config.AppConfig
	ds             *dbservice.DatabaseService
	rs             *redisservice.RedisService
	analyticsModel *AnalyticsModel
	natsService    *natsservice.NatsService
}

func NewPollModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService) *PollModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.New(app.DB)
	}
	if rs == nil {
		rs = redisservice.New(app.RDS)
	}

	return &PollModel{
		app:            app,
		ds:             ds,
		rs:             rs,
		analyticsModel: NewAnalyticsModel(app, ds, rs),
		natsService:    natsservice.New(app),
	}
}
