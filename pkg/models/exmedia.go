package models

import (
	"fmt"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

type ExMediaModel struct {
	app            *config.AppConfig
	ds             *dbservice.DatabaseService
	rs             *redisservice.RedisService
	lk             *livekitservice.LivekitService
	natsService    *natsservice.NatsService
	analyticsModel *AnalyticsModel
	logger         *logrus.Entry
}

type updateRoomMetadataOpts struct {
	isActive *bool
	sharedBy *string
	url      *string
}

func NewExMediaModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, natsService *natsservice.NatsService, analyticsModel *AnalyticsModel, logger *logrus.Logger) *ExMediaModel {
	return &ExMediaModel{
		app:            app,
		ds:             ds,
		rs:             rs,
		natsService:    natsService,
		analyticsModel: analyticsModel,
		logger:         logger.WithField("model", "external-media"),
	}
}

func (m *ExMediaModel) HandleTask(req *plugnmeet.ExternalMediaPlayerReq) error {
	switch req.Task {
	case plugnmeet.ExternalMediaPlayerTask_START_PLAYBACK:
		return m.startPlayBack(req)
	case plugnmeet.ExternalMediaPlayerTask_END_PLAYBACK:
		return m.endPlayBack(req)
	}

	return fmt.Errorf("not valid request")
}
