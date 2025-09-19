package models

import (
	"context"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/sirupsen/logrus"
)

type FileModel struct {
	ctx         context.Context
	app         *config.AppConfig
	ds          *dbservice.DatabaseService
	natsService *natsservice.NatsService
	logger      *logrus.Entry
}

func NewFileModel(ctx context.Context, app *config.AppConfig, ds *dbservice.DatabaseService, natsService *natsservice.NatsService, logger *logrus.Logger) *FileModel {
	return &FileModel{
		ctx:         ctx,
		app:         app,
		ds:          ds,
		natsService: natsService,
		logger:      logger.WithField("model", "file"),
	}
}
