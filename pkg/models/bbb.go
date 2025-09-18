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
	rrm    *RecordingModel
	logger *logrus.Entry
}

func NewBBBApiWrapperModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, rrm *RecordingModel, logger *logrus.Logger) *BBBApiWrapperModel {
	return &BBBApiWrapperModel{
		app:    app,
		ds:     ds,
		rs:     rs,
		rrm:    rrm,
		logger: logger.WithField("model", "bbb"),
	}
}
