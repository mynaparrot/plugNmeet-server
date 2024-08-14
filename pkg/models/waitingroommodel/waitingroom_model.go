package waitingroommodel

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekitservice"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redisservice"
)

type WaitingRoomModel struct {
	app *config.AppConfig
	rs  *redisservice.RedisService
	lk  *livekitservice.LivekitService
}

func New(app *config.AppConfig, rs *redisservice.RedisService, lk *livekitservice.LivekitService) *WaitingRoomModel {
	if app == nil {
		app = config.GetConfig()
	}
	if rs == nil {
		rs = redisservice.New(app.RDS)
	}
	if lk == nil {
		lk = livekitservice.New(app, rs)
	}

	return &WaitingRoomModel{
		app: app,
		rs:  rs,
		lk:  lk,
	}
}
