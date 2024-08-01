package bbbmodel

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/dbservice"
)

type BBBApiWrapperModel struct {
	app *config.AppConfig
	ds  *dbservice.DatabaseService
}

func New(app *config.AppConfig, ds *dbservice.DatabaseService) *BBBApiWrapperModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.NewDBService(app.ORM)
	}

	return &BBBApiWrapperModel{
		app: app,
		ds:  ds,
	}
}
