package controllers

import (
	"encoding/json"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
)

type InsightsController struct {
	app           *config.AppConfig
	sub           *nats.Subscription
	natsService   *natsservice.NatsService
	logger        *logrus.Entry
	insightsModel *models.InsightsModel
}

func NewInsightsController(app *config.AppConfig, natsService *natsservice.NatsService, im *models.InsightsModel, logger *logrus.Logger) *InsightsController {
	return &InsightsController{
		app:           app,
		natsService:   natsService,
		insightsModel: im,
		logger:        logger.WithField("controller", "insights"),
	}
}

// SubscribeToAgentTaskRequests is the central handler for all incoming agent tasks.
func (i *InsightsController) SubscribeToAgentTaskRequests() {
	sub, err := i.app.NatsConn.Subscribe(models.InsightsNatsChannel, func(msg *nats.Msg) {
		var payload models.InsightsTaskPayload
		err := json.Unmarshal(msg.Data, &payload)
		if err != nil {
			i.logger.WithError(err).Error("failed to unmarshal insights task payload")
			return
		}

		i.logger.Infof("received task '%s' for service '%s' in room '%s'", payload.Task, payload.ServiceName, payload.RoomName)
		i.insightsModel.HandleIncomingAgentTask(&payload)
	})
	if err != nil {
		i.logger.WithError(err).Fatalln("failed to subscribe to NATS for insights tasks")
	}
	i.logger.Infof("successfully connected with %s channel", sub.Subject)
	i.sub = sub
}

func (i *InsightsController) Shutdown() {
	if i.sub != nil {
		if err := i.sub.Unsubscribe(); err != nil {
			i.logger.WithError(err).Errorln("failed to unsubscribe from NATS")
		}
	}
	i.insightsModel.Shutdown()
}

func (i *InsightsController) HandleTranscriptionConfigure(c *fiber.Ctx) error {
	if i.app.Insights == nil {
		return utils.SendCommonProtobufResponse(c, false, "insights feature wasn't configured")
	}
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")

	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	/*metadataStruct, err := i.natsService.GetRoomMetadataStruct(roomId.(string))
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}
	if !metadataStruct.RoomFeatures.InsightsFeatures.IsAllow || !metadataStruct.RoomFeatures.InsightsFeatures.TranscriptionFeatures.IsAllow {
		return utils.SendCommonProtobufResponse(c, false, "insights feature wasn't enabled")
	}*/
	err := i.insightsModel.ConfigureAgentTask("transcription", roomId.(string), []string{requestedUserId.(string)})
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}
	time.Sleep(time.Second * 5)

	err = i.insightsModel.ActivateAgentTaskForUser("transcription", roomId.(string), requestedUserId.(string), nil, nil)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, false, "success")
}

func (i *InsightsController) HandleEndTranscription(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")

	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	err := i.insightsModel.EndRoomAgentTaskByServiceName("transcription", roomId.(string))
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, false, "success")
}
