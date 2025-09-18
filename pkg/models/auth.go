package models

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/sirupsen/logrus"
)

type AuthModel struct {
	app         *config.AppConfig
	natsService *natsservice.NatsService
	logger      *logrus.Entry
}

func NewAuthModel(app *config.AppConfig, natsService *natsservice.NatsService, logger *logrus.Logger) *AuthModel {
	return &AuthModel{
		app:         app,
		natsService: natsService,
		logger:      logger.WithField("model", "auth"),
	}
}
