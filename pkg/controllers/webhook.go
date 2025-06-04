package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
)

// HandleWebhook processes incoming webhook events from LiveKit
func HandleWebhook(c *fiber.Ctx) error {
	// Read raw request body
	data := c.Body()

	// Extract Authorization header
	token := c.Get("Authorization")
	if token == "" {
		return c.SendStatus(fiber.StatusForbidden)
	}

	// Validate the webhook token using LiveKit secret
	authModel := models.NewAuthModel(nil, nil)
	if _, err := authModel.ValidateLivekitWebhookToken(data, token); err != nil {
		return c.SendStatus(fiber.StatusForbidden)
	}

	// Unmarshal the webhook event
	event := new(livekit.WebhookEvent)
	if err := op.Unmarshal(data, event); err != nil {
		return c.SendStatus(fiber.StatusUnprocessableEntity)
	}

	// Handle the webhook event asynchronously
	webhookModel := models.NewWebhookModel(nil, nil, nil)
	go webhookModel.HandleWebhookEvents(event)

	return c.SendStatus(fiber.StatusOK)
}
