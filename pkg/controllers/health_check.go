package controllers

import (
	"context"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

func HandleHealthCheck(c *fiber.Ctx) error {
	err := config.AppCnf.DB.Ping()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString("DB connection error")
	}

	_, err = config.AppCnf.RDS.Ping(context.Background()).Result()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString("Redis connection error")
	}

	return c.Status(fiber.StatusOK).SendString("Healthy")
}
