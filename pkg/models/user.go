package models

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

type UserModel struct {
	app            *config.AppConfig
	ds             *dbservice.DatabaseService
	rs             *redisservice.RedisService
	lk             *livekitservice.LivekitService
	natsService    *natsservice.NatsService
	analyticsModel *AnalyticsModel
	logger         *logrus.Entry
}

func NewUserModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, lk *livekitservice.LivekitService, natsService *natsservice.NatsService, analyticsModel *AnalyticsModel, logger *logrus.Logger) *UserModel {
	return &UserModel{
		app:            app,
		ds:             ds,
		rs:             rs,
		lk:             lk,
		natsService:    natsService,
		analyticsModel: analyticsModel,
		logger:         logger.WithField("model", "user"),
	}
}

func (m *UserModel) CommonValidation(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")
	if isAdmin != true {
		return errors.New(config.OnlyAdminCanRequest)
	}
	if roomId == "" {
		return errors.New(config.NoRoomIdInToken)
	}

	return nil
}
