package controllers

import "github.com/gofiber/fiber/v2"

func HandleHealthCheck(c *fiber.Ctx) error {
	return c.Status(fiber.StatusOK).SendString("Healthy")
}
