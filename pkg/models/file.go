package models

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
)

type FileModel struct {
	app         *config.AppConfig
	ds          *dbservice.DatabaseService
	natsService *natsservice.NatsService
}

func NewFileModel(app *config.AppConfig, ds *dbservice.DatabaseService, natsService *natsservice.NatsService) *FileModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.New(app.DB)
	}
	if natsService == nil {
		natsService = natsservice.New(app)
	}

	return &FileModel{
		app:         app,
		ds:          ds,
		natsService: natsService,
	}
}
