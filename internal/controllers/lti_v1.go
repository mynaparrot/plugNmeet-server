package controllers

import (
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugNmeet/internal/models"
)

func HandleLTIV1(c *fiber.Ctx) error {
	b := make([]byte, len(c.Body()))
	copy(b, c.Body())

	if len(b) == 0 {
		return c.Status(fiber.StatusUnauthorized).SendString("empty body")
	}

	signingURL := fmt.Sprintf("%v://%v%v", c.Protocol(), c.Hostname(), c.OriginalURL())
	m := models.NewLTIV1Model()
	params, err := m.VerifyAuth(string(b), signingURL)
	if err != nil {
		return err
	}

	roomId := fmt.Sprintf("%s_%s_%s", params.Get("tool_consumer_instance_guid"), params.Get("context_id"), params.Get("resource_link_id"))

	fmt.Println(roomId)

	return nil
}
