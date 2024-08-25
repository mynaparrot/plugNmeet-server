package waitingroommodel

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

type WaitingRoomModel struct {
	app         *config.AppConfig
	rs          *redisservice.RedisService
	natsService *natsservice.NatsService
}

func New(app *config.AppConfig, rs *redisservice.RedisService) *WaitingRoomModel {
	if app == nil {
		app = config.GetConfig()
	}
	if rs == nil {
		rs = redisservice.New(app.RDS)
	}

	return &WaitingRoomModel{
		app:         app,
		rs:          rs,
		natsService: natsservice.New(app),
	}
}
