package models

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

type BBBApiWrapperModel struct {
	app *config.AppConfig
	ds  *dbservice.DatabaseService
	rs  *redisservice.RedisService
}

func NewBBBApiWrapperModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService) *BBBApiWrapperModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.New(app.DB)
	}
	if rs == nil {
		rs = redisservice.New(app.RDS)
	}

	return &BBBApiWrapperModel{
		app: app,
		ds:  ds,
		rs:  rs,
	}
}
