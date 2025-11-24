package controllers

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
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
		// Pass the raw message directly to the model's handler.
		// The model is now responsible for unmarshalling and replying.
		i.insightsModel.HandleIncomingAgentTask(msg)
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

	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	req := new(plugnmeet.InsightsTranscriptionConfigReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	err = i.insightsModel.TranscriptionConfigure(req, roomId.(string))
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

func (i *InsightsController) HandleTranscriptionUserSession(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")

	req := new(plugnmeet.InsightsTranscriptionUserSessionReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	err = i.insightsModel.TranscriptionUserSession(req, roomId.(string), requestedUserId.(string))
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

func (i *InsightsController) HandleEndTranscription(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")

	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	err := i.insightsModel.EndTranscription(roomId.(string))
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

func (i *InsightsController) HandleGetTranscriptionUserTaskStatus(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")

	res, err := i.insightsModel.GetUserTaskStatus(insights.ServiceTypeTranscription, roomId.(string), requestedUserId.(string), time.Second*5)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	c.Set("Content-Type", "application/protobuf")
	return c.Send(res)
}

func (i *InsightsController) HandleGetSupportedLangs(c *fiber.Ctx) error {
	req := new(plugnmeet.InsightsGetSupportedLanguagesReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	serviceType, err := insights.ToServiceType(req.ServiceType)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	langs, err := i.insightsModel.GetSupportedLanguagesForService(serviceType)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	res := &plugnmeet.InsightsGetSupportedLanguagesRes{
		Status:    true,
		Msg:       "success",
		Languages: langs,
	}

	return utils.SendProtobufResponse(c, res)
}

func (i *InsightsController) HandleChatTranslationConfigure(c *fiber.Ctx) error {
	if i.app.Insights == nil {
		return utils.SendCommonProtobufResponse(c, false, "insights feature wasn't configured")
	}
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")

	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	req := new(plugnmeet.InsightsChatTranslationConfigReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	err = i.insightsModel.ChatTranslationConfigure(req, roomId.(string))
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

func (i *InsightsController) HandleExecuteChatTranslation(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")

	req := new(plugnmeet.InsightsTranslateTextReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	result, err := i.insightsModel.ExecuteChatTranslation(c.UserContext(), req, roomId.(string), requestedUserId.(string))
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendProtobufResponse(c, result)
}

func (i *InsightsController) HandleEndChatTranslation(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")

	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}
	err := i.insightsModel.ChatEndTranslation(roomId.(string))
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}
	return utils.SendCommonProtobufResponse(c, true, "success")
}
