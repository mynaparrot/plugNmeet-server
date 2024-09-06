package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"google.golang.org/protobuf/encoding/protojson"
)

func HandleWebhook(c *fiber.Ctx) error {
	data := c.Body()
	token := c.Request().Header.Peek("Authorization")

	if len(token) == 0 {
		return c.SendStatus(fiber.StatusForbidden)
	}

	m := models.NewAuthModel(nil, nil)
	// here request is coming from livekit, so
	// we'll use livekit secret to validate
	_, err := m.ValidateLivekitWebhookToken(data, string(token))
	if err != nil {
		return c.SendStatus(fiber.StatusForbidden)
	}

	op := protojson.UnmarshalOptions{
		DiscardUnknown: true,
	}
	event := new(livekit.WebhookEvent)
	if err = op.Unmarshal(data, event); err != nil {
		return c.SendStatus(fiber.StatusForbidden)
	}

	mm := models.NewWebhookModel(nil, nil, nil)
	go mm.HandleWebhookEvents(event)

	return c.SendStatus(fiber.StatusOK)
}
