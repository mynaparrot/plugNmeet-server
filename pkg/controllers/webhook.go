package controllers

import (
	"github.com/gammazero/workerpool"
	"github.com/gofiber/fiber/v3"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"go.uber.org/fx"
)

const (
	// WebhookMaxWorkers sets the maximum number of concurrent workers for processing webhooks.
	WebhookMaxWorkers = 100
)

// WebhookController holds dependencies for webhook-related handlers.
type WebhookController struct {
	AuthModel    *models.AuthModel
	WebhookModel *models.WebhookModel
	wp           *workerpool.WorkerPool
}

type WebhookControllerArgs struct {
	fx.In
	AuthModel    *models.AuthModel
	WebhookModel *models.WebhookModel
}

// NewWebhookController creates a new WebhookController.
func NewWebhookController(args WebhookControllerArgs) *WebhookController {
	return &WebhookController{
		AuthModel:    args.AuthModel,
		WebhookModel: args.WebhookModel,
		wp:           workerpool.New(WebhookMaxWorkers),
	}
}

// Shutdown stops the worker pool gracefully.
func (wc *WebhookController) Shutdown() {
	wc.wp.Stop()
}

// HandleWebhook processes incoming webhook events from LiveKit.
func (wc *WebhookController) HandleWebhook(c fiber.Ctx) error {
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

	// Handle the webhook event asynchronously in the worker pool
	wc.wp.Submit(func() {
		wc.WebhookModel.HandleWebhookEvents(event)
	})

	return c.SendStatus(fiber.StatusOK)
}
