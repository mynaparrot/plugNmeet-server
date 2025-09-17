package controllers

import (
	"context"

	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

func HandleHealthCheck(c *fiber.Ctx) error {
	db, err := config.GetConfig().DB.DB()
	if err != nil {
		return c.Status(fiber.StatusServiceUnavailable).SendString("DB connection error")
	}
	err = db.Ping()
	if err != nil {
		return c.Status(fiber.StatusServiceUnavailable).SendString("DB connection error")
	}

	_, err = config.GetConfig().RDS.Ping(context.Background()).Result()
	if err != nil {
		return c.Status(fiber.StatusServiceUnavailable).SendString("Redis connection error")
	}

	if !config.GetConfig().NatsConn.IsConnected() {
		return c.Status(fiber.StatusServiceUnavailable).SendString("Nats connection error")
	}

	return c.Status(fiber.StatusOK).SendString("Healthy")
}
