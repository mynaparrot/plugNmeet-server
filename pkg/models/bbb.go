package models

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

type BBBApiWrapperModel struct {
	app    *config.AppConfig
	ds     *dbservice.DatabaseService
	rs     *redisservice.RedisService
	logger *logrus.Entry
}

func NewBBBApiWrapperModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, logger *logrus.Logger) *BBBApiWrapperModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.New(app.DB, logger)
	}
	if rs == nil {
		rs = redisservice.New(app.RDS, logger)
	}

	return &BBBApiWrapperModel{
		app:    app,
		ds:     ds,
		rs:     rs,
		logger: logger.WithField("model", "bbb"),
	}
}
