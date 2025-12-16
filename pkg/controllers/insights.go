package controllers

import (
	"encoding/json"
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
	app             *config.AppConfig
	agentTaskSub    *nats.Subscription
	summarizeJobSub *nats.Subscription
	natsService     *natsservice.NatsService
	logger          *logrus.Entry
	insightsModel   *models.InsightsModel
}

func NewInsightsController(app *config.AppConfig, natsService *natsservice.NatsService, im *models.InsightsModel, logger *logrus.Logger) *InsightsController {
	return &InsightsController{
		app:           app,
		natsService:   natsService,
		insightsModel: im,
		logger:        logger.WithField("controller", "insights"),
	}
}

func (i *InsightsController) StartSubscription() {
	go i.SubscribeToAgentTaskRequests()
	go i.SubscribeToSummarizeJobs()
}

// SubscribeToAgentTaskRequests is the central handler for all incoming agent tasks.
func (i *InsightsController) SubscribeToAgentTaskRequests() {
	sub, err := i.app.NatsConn.Subscribe(insights.InsightsNatsChannel, func(msg *nats.Msg) {
		// Pass the raw message directly to the model's handler.
		// The model is now responsible for unmarshalling and replying.
		i.insightsModel.HandleIncomingAgentTask(msg)
	})
	if err != nil {
		i.logger.WithError(err).Fatalln("failed to subscribe to NATS for insights tasks")
	}
	i.logger.Infof("successfully connected with %s channel", sub.Subject)
	i.agentTaskSub = sub
}

// SubscribeToSummarizeJobs sets up a queue subscription to handle summarization jobs.
func (i *InsightsController) SubscribeToSummarizeJobs() {
	sub, err := i.app.NatsConn.QueueSubscribe(insights.SummarizeJobQueue, "pnm-summarize-worker-group", func(msg *nats.Msg) {
		var payload insights.SummarizeJobPayload
		err := json.Unmarshal(msg.Data, &payload)
		if err != nil {
			i.logger.WithError(err).Error("failed to unmarshal summarize job payload")
			return
		}
		// Pass the payload to a new model method for processing.
		i.insightsModel.StartProcessingSummarizeJob(&payload)
	})
	if err != nil {
		i.logger.WithError(err).Fatalln("failed to subscribe to NATS for summarize jobs")
	}
	i.logger.Infof("successfully connected with %s queue", sub.Subject)
	i.summarizeJobSub = sub
}

func (i *InsightsController) Shutdown() {
	if i.agentTaskSub != nil {
		if err := i.agentTaskSub.Unsubscribe(); err != nil {
			i.logger.WithError(err).Errorln("failed to unsubscribe from agent tasks")
		}
	}
	if i.summarizeJobSub != nil {
		if err := i.summarizeJobSub.Unsubscribe(); err != nil {
			i.logger.WithError(err).Errorln("failed to unsubscribe from summarize jobs")
		}
	}
	i.insightsModel.Shutdown()
}

func (i *InsightsController) HandleTranscriptionConfigure(c *fiber.Ctx) error {
	if i.app.Insights == nil || !i.app.Insights.Enabled {
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

	langs, err := i.insightsModel.GetSupportedLanguagesForService(c.UserContext(), serviceType)
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
	if i.app.Insights == nil || !i.app.Insights.Enabled {
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

func (i *InsightsController) HandleAITextChatConfigure(c *fiber.Ctx) error {
	if i.app.Insights == nil || !i.app.Insights.Enabled {
		return utils.SendCommonProtobufResponse(c, false, "insights feature wasn't configured")
	}
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")

	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	req := new(plugnmeet.InsightsAITextChatConfigReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	err = i.insightsModel.AITextChatConfigure(req, roomId.(string))
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

func (i *InsightsController) HandleExecuteAITextChat(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")

	req := new(plugnmeet.InsightsAITextChatContent)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	err = i.insightsModel.ExecuteAITextChat(req, roomId.(string), requestedUserId.(string))
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

func (i *InsightsController) HandleEndAITextChat(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")

	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	err := i.insightsModel.EndAITextChat(roomId.(string))
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}
	return utils.SendCommonProtobufResponse(c, true, "success")
}

func (i *InsightsController) HandleAIMeetingSummarizationConfig(c *fiber.Ctx) error {
	if i.app.Insights == nil || !i.app.Insights.Enabled {
		return utils.SendCommonProtobufResponse(c, false, "insights feature wasn't configured")
	}
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")

	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	req := new(plugnmeet.InsightsAIMeetingSummarizationConfigReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	err = i.insightsModel.AIMeetingSummarizationConfig(req, roomId.(string))
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

func (i *InsightsController) HandleEndAIMeetingSummarization(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")

	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	err := i.insightsModel.EndEndAIMeetingSummarization(roomId.(string))
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}
	return utils.SendCommonProtobufResponse(c, true, "success")
}
