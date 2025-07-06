package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
)

// WebhookController holds dependencies for webhook-related handlers.
type WebhookController struct {
	AuthModel    *models.AuthModel
	WebhookModel *models.WebhookModel
}

// NewWebhookController creates a new WebhookController.
func NewWebhookController(authModel *models.AuthModel, webhookModel *models.WebhookModel) *WebhookController {
	return &WebhookController{
		AuthModel:    authModel,
		WebhookModel: webhookModel,
	}
}

// HandleWebhook processes incoming webhook events from LiveKit.
func (wc *WebhookController) HandleWebhook(c *fiber.Ctx) error {
	// Read raw request body
	data := c.Body()

	// Extract Authorization header
	token := c.Get("Authorization")
	if token == "" {
		return c.SendStatus(fiber.StatusForbidden)
	}

	// Validate the webhook token using LiveKit secret
	if _, err := wc.AuthModel.ValidateLivekitWebhookToken(data, token); err != nil {
		return c.SendStatus(fiber.StatusForbidden)
	}

	// Unmarshal the webhook event
	event := new(livekit.WebhookEvent)
	if err := unmarshalOpts.Unmarshal(data, event); err != nil {
		return c.SendStatus(fiber.StatusUnprocessableEntity)
	}

	// Handle the webhook event asynchronously
	go wc.WebhookModel.HandleWebhookEvents(event)

	return c.SendStatus(fiber.StatusOK)
}
