package models

import (
	"errors"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

type ExMediaModel struct {
	app         *config.AppConfig
	ds          *dbservice.DatabaseService
	rs          *redisservice.RedisService
	lk          *livekitservice.LivekitService
	natsService *natsservice.NatsService
}

type updateRoomMetadataOpts struct {
	isActive *bool
	sharedBy *string
	url      *string
}

func NewExMediaModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService) *ExMediaModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.New(app.DB)
	}
	if rs == nil {
		rs = redisservice.New(app.RDS)
	}

	return &ExMediaModel{
		app:         app,
		ds:          ds,
		rs:          rs,
		natsService: natsservice.New(app),
	}
}

func (m *ExMediaModel) HandleTask(req *plugnmeet.ExternalMediaPlayerReq) error {
	switch req.Task {
	case plugnmeet.ExternalMediaPlayerTask_START_PLAYBACK:
		return m.startPlayBack(req)
	case plugnmeet.ExternalMediaPlayerTask_END_PLAYBACK:
		return m.endPlayBack(req)
	}

	return errors.New("not valid request")
}
