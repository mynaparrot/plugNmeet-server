package analyticsmodel

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/dbservice"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekitservice"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redisservice"
	"sync"
)

type AnalyticsModel struct {
	sync.RWMutex
	data *plugnmeet.AnalyticsDataMsg
	app  *config.AppConfig
	ds   *dbservice.DatabaseService
	rs   *redisservice.RedisService
	lk   *livekitservice.LivekitService
}

func New(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, lk *livekitservice.LivekitService) *AnalyticsModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.NewDBService(app.ORM)
	}
	if rs == nil {
		rs = redisservice.NewRedisService(app.RDS)
	}
	if lk == nil {
		lk = livekitservice.NewLivekitService(rs)
	}

	return &AnalyticsModel{
		app: config.GetConfig(),
		ds:  ds,
		rs:  rs,
	}
}
