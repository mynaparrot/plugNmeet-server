package controllers

import (
	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"go.uber.org/fx"
	"gorm.io/gorm"
)

type HealthCheckController struct {
	app *config.AppConfig
	rds *redis.Client
	db  *gorm.DB
	nc  *nats.Conn
}

type HealthCheckControllerArgs struct {
	fx.In
	App      *config.AppConfig
	RDS      *redis.Client
	DB       *gorm.DB
	NatsConn *nats.Conn
}

func NewHealthCheckController(args HealthCheckControllerArgs) *HealthCheckController {
	return &HealthCheckController{
		app: args.App,
		rds: args.RDS,
		db:  args.DB,
		nc:  args.NatsConn,
	}
}

func (h *HealthCheckController) HandleHealthCheck(c fiber.Ctx) error {
	db, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusServiceUnavailable).SendString("DB connection error")
	}
	if err := db.PingContext(c.RequestCtx()); err != nil {
		return c.Status(fiber.StatusServiceUnavailable).SendString("DB connection error")
	}

	if _, err := h.rds.Ping(c.RequestCtx()).Result(); err != nil {
		return c.Status(fiber.StatusServiceUnavailable).SendString("Redis connection error")
	}

	if !h.nc.IsConnected() {
		return c.Status(fiber.StatusServiceUnavailable).SendString("Nats connection error")
	}

	return c.Status(fiber.StatusOK).SendString("Healthy")
}
