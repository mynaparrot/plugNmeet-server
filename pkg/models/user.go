package models

import (
	"errors"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

type UserModel struct {
	app         *config.AppConfig
	ds          *dbservice.DatabaseService
	rs          *redisservice.RedisService
	lk          *livekitservice.LivekitService
	natsService *natsservice.NatsService
}

func NewUserModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService) *UserModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.New(app.DB)
	}
	if rs == nil {
		rs = redisservice.New(app.RDS)
	}

	return &UserModel{
		app:         app,
		ds:          ds,
		rs:          rs,
		lk:          livekitservice.New(app),
		natsService: natsservice.New(app),
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
