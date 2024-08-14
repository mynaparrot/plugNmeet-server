package bbbmodel

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/dbservice"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekitservice"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redisservice"
)

type BBBApiWrapperModel struct {
	app *config.AppConfig
	ds  *dbservice.DatabaseService
	rs  *redisservice.RedisService
	lk  *livekitservice.LivekitService
}

func New(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, lk *livekitservice.LivekitService) *BBBApiWrapperModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.New(app.ORM)
	}
	if rs == nil {
		rs = redisservice.New(app.RDS)
	}
	if lk == nil {
		lk = livekitservice.New(app, rs)
	}

	return &BBBApiWrapperModel{
		app: app,
		ds:  ds,
		rs:  rs,
		lk:  lk,
	}
}
