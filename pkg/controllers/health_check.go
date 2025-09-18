package controllers

import (
	"context"

	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

type HealthCheckController struct {
	app *config.AppConfig
}

func NewHealthCheckController(app *config.AppConfig) *HealthCheckController {
	return &HealthCheckController{app: app}
}

func (h *HealthCheckController) HandleHealthCheck(c *fiber.Ctx) error {
	db, err := h.app.DB.DB()
	if err != nil {
		return c.Status(fiber.StatusServiceUnavailable).SendString("DB connection error")
	}
	err = db.PingContext(c.Context())
	if err != nil {
		return c.Status(fiber.StatusServiceUnavailable).SendString("DB connection error")
	}

	_, err = h.app.RDS.Ping(context.Background()).Result()
	if err != nil {
		return c.Status(fiber.StatusServiceUnavailable).SendString("Redis connection error")
	}

	if !h.app.NatsConn.IsConnected() {
		return c.Status(fiber.StatusServiceUnavailable).SendString("Nats connection error")
	}

	return c.Status(fiber.StatusOK).SendString("Healthy")
}
