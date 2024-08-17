package authmodel

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/natsservice"
)

type AuthModel struct {
	app         *config.AppConfig
	natsService *natsservice.NatsService
}

func New(app *config.AppConfig, natsService *natsservice.NatsService) *AuthModel {
	if app == nil {
		app = config.GetConfig()
	}
	if natsService == nil {
		natsService = natsservice.New(app)
	}

	return &AuthModel{
		app:         config.GetConfig(),
		natsService: natsService,
	}
}
