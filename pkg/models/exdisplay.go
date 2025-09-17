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
	app         *config.AppConfig
	ds          *dbservice.DatabaseService
	rs          *redisservice.RedisService
	lk          *livekitservice.LivekitService
	natsService *natsservice.NatsService
	logger      *logrus.Entry
}

func NewExDisplayModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, logger *logrus.Logger) *ExDisplayModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.New(app.DB, logger)
	}
	if rs == nil {
		rs = redisservice.New(app.RDS, logger)
	}

	return &ExDisplayModel{
		app:         app,
		ds:          ds,
		rs:          rs,
		natsService: natsservice.New(app, logger),
		logger:      logger.WithField("model", "external-display"),
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
