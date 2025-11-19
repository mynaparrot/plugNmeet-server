package controllers

import (
	"encoding/json"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
)

type InsightsController struct {
	app           *config.AppConfig
	sub           *nats.Subscription
	logger        *logrus.Entry
	insightsModel *models.InsightsModel
}

func NewInsightsController(app *config.AppConfig, im *models.InsightsModel, logger *logrus.Logger) *InsightsController {
	return &InsightsController{
		app:           app,
		insightsModel: im,
		logger:        logger.WithField("controller", "insights"),
	}
}

// SubscribeToAgentTaskRequests is the central handler for all incoming agent tasks.
func (c *InsightsController) SubscribeToAgentTaskRequests() {
	sub, err := c.app.NatsConn.Subscribe(models.InsightsNatsChannel, func(msg *nats.Msg) {
		var payload models.InsightsTaskPayload
		err := json.Unmarshal(msg.Data, &payload)
		if err != nil {
			c.logger.WithError(err).Error("failed to unmarshal insights task payload")
			return
		}

		c.logger.Infof("received task '%s' for service '%s' in room '%s'", payload.Task, payload.ServiceName, payload.RoomName)
		c.insightsModel.HandleIncomingAgentTask(&payload)
	})
	if err != nil {
		c.logger.WithError(err).Fatalln("failed to subscribe to NATS for insights tasks")
	}
	c.logger.Infof("successfully connected with %s channel", sub.Subject)
	c.sub = sub
}

func (c *InsightsController) Shutdown() {
	if c.sub != nil {
		if err := c.sub.Unsubscribe(); err != nil {
			c.logger.WithError(err).Errorln("failed to unsubscribe from NATS")
		}
	}
	c.insightsModel.Shutdown()
}
