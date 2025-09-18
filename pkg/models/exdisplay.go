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

type ExDisplayModel struct {
	app            *config.AppConfig
	ds             *dbservice.DatabaseService
	rs             *redisservice.RedisService
	lk             *livekitservice.LivekitService
	natsService    *natsservice.NatsService
	analyticsModel *AnalyticsModel
	logger         *logrus.Entry
}

func NewExDisplayModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, natsService *natsservice.NatsService, analyticsModel *AnalyticsModel, logger *logrus.Logger) *ExDisplayModel {
	return &ExDisplayModel{
		app:            app,
		ds:             ds,
		rs:             rs,
		natsService:    natsService,
		analyticsModel: analyticsModel,
		logger:         logger.WithField("model", "external-display"),
	}
}

func (m *ExDisplayModel) HandleTask(req *plugnmeet.ExternalDisplayLinkReq) error {
	switch req.Task {
	case plugnmeet.ExternalDisplayLinkTask_START_EXTERNAL_LINK:
		return m.start(req)
	case plugnmeet.ExternalDisplayLinkTask_STOP_EXTERNAL_LINK:
		return m.end(req)
	}

	return fmt.Errorf("not valid request")
}
