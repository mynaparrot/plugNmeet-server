package analyticsmodel

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/dbservice"
)

type AnalyticsAuthModel struct {
	app *config.AppConfig
	ds  *dbservice.DatabaseService
}

func New(app *config.AppConfig, ds *dbservice.DatabaseService) *AnalyticsAuthModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.NewDBService(app.ORM)
	}

	return &AnalyticsAuthModel{
		app: config.GetConfig(),
		ds:  ds,
	}
}
