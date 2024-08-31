package models

import (
	"errors"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	log "github.com/sirupsen/logrus"
	"time"
)

type UserModel struct {
	app         *config.AppConfig
	ds          *dbservice.DatabaseService
	rs          *redisservice.RedisService
	lk          *livekitservice.LivekitService
	natsService *natsservice.NatsService
}

func NewUserModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, lk *livekitservice.LivekitService) *UserModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.New(app.DB)
	}
	if rs == nil {
		rs = redisservice.New(app.RDS)
	}
	if lk == nil {
		lk = livekitservice.New(app, rs)
	}

	return &UserModel{
		app:         app,
		ds:          ds,
		rs:          rs,
		lk:          lk,
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

// CheckAndWaitUntilRoomCreationInProgress will check the process & wait if needed
func (m *UserModel) CheckAndWaitUntilRoomCreationInProgress(roomId string) {
	for {
		locked := m.natsService.IsRoomCreationLock(roomId)
		if locked {
			log.Println(roomId, "joining not possible because of room locked, waiting for:", config.WaitDurationIfRoomCreationLocked)
			time.Sleep(config.WaitDurationIfRoomCreationLocked)
		} else {
			break
		}
	}
}
