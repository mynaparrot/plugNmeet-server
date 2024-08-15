package natsmodel

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/dbservice"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/natsservice"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redisservice"
)

type NatsModel struct {
	app         *config.AppConfig
	ds          *dbservice.DatabaseService
	rs          *redisservice.RedisService
	natsService *natsservice.NatsService
}

func New(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService) *NatsModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.New(app.ORM)
	}
	if rs == nil {
		rs = redisservice.New(app.RDS)
	}

	return &NatsModel{
		app:         app,
		ds:          ds,
		rs:          rs,
		natsService: natsservice.New(app, rs),
	}
}
