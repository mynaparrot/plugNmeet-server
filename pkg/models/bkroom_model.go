package models

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

type BreakoutRoomModel struct {
	app         *config.AppConfig
	ds          *dbservice.DatabaseService
	rs          *redisservice.RedisService
	lk          *livekitservice.LivekitService
	rm          *RoomModel
	natsService *natsservice.NatsService
}

func NewBreakoutRoomModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService) *BreakoutRoomModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.New(app.DB)
	}
	if rs == nil {
		rs = redisservice.New(app.RDS)
	}

	return &BreakoutRoomModel{
		app:         app,
		ds:          ds,
		rs:          rs,
		rm:          NewRoomModel(app, ds, rs, nil),
		natsService: natsservice.New(app),
	}
}
