package ingressmodel

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

type IngressModel struct {
	app *config.AppConfig
	ds  *dbservice.DatabaseService
	rs  *redisservice.RedisService
	lk  *livekitservice.LivekitService
}

func New(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, lk *livekitservice.LivekitService) *IngressModel {
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

	return &IngressModel{
		app: app,
		ds:  ds,
		rs:  rs,
		lk:  lk,
	}
}
