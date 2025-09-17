package models

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/sirupsen/logrus"
)

type FileModel struct {
	app         *config.AppConfig
	ds          *dbservice.DatabaseService
	natsService *natsservice.NatsService
	logger      *logrus.Entry
}

func NewFileModel(app *config.AppConfig, ds *dbservice.DatabaseService, natsService *natsservice.NatsService, logger *logrus.Logger) *FileModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.New(app.DB, logger)
	}
	if natsService == nil {
		natsService = natsservice.New(app, logger)
	}

	return &FileModel{
		app:         app,
		ds:          ds,
		natsService: natsService,
		logger:      logger.WithField("model", "file"),
	}
}
