package recordingmodel

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/dbservice"
)

type AuthRecording struct {
	app *config.AppConfig
	ds  *dbservice.DatabaseService
}

func New(app *config.AppConfig, ds *dbservice.DatabaseService) *AuthRecording {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.NewDBService(app.ORM)
	}

	return &AuthRecording{
		app: config.GetConfig(),
		ds:  ds,
	}
}
