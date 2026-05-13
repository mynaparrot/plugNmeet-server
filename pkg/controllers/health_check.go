package controllers

import (
	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

type HealthCheckController struct {
	app *config.AppConfig
}

func NewHealthCheckController(app *config.AppConfig) *HealthCheckController {
	return &HealthCheckController{app: app}
}

func (h *HealthCheckController) HandleHealthCheck(c fiber.Ctx) error {
	db, err := h.app.DB.DB()
	if err != nil {
		return c.Status(fiber.StatusServiceUnavailable).SendString("DB connection error")
	}
	if err := db.PingContext(c.RequestCtx()); err != nil {
		return c.Status(fiber.StatusServiceUnavailable).SendString("DB connection error")
	}

	if _, err := h.app.RDS.Ping(c.RequestCtx()).Result(); err != nil {
		return c.Status(fiber.StatusServiceUnavailable).SendString("Redis connection error")
	}

	if !h.app.NatsConn.IsConnected() {
		return c.Status(fiber.StatusServiceUnavailable).SendString("Nats connection error")
	}

	return c.Status(fiber.StatusOK).SendString("Healthy")
}
