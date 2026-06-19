package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
)

type InsightsController struct {
	app             *config.AppConfig
	agentTaskSub    *nats.Subscription
	summarizeJobSub jetstream.ConsumeContext
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

// Initialize performs the synchronous setup for the NATS subscriptions.
func (i *InsightsController) Initialize(ctx context.Context) error {
	if err := i.subscribeToAgentTaskRequests(); err != nil {
		return err
	}
	if err := i.subscribeToSummarizeJobs(ctx); err != nil {
		return err
	}
	return nil
}

// subscribeToAgentTaskRequests is the central handler for all incoming agent tasks.
func (i *InsightsController) subscribeToAgentTaskRequests() error {
	sub, err := i.app.NatsConn.Subscribe(insights.InsightsNatsChannel, func(msg *nats.Msg) {
		// Pass the raw message directly to the model's handler.
		// The model is now responsible for unmarshalling and replying.
		i.insightsModel.HandleIncomingAgentTask(msg)
	})
	if err != nil {
		return fmt.Errorf("failed to subscribe to NATS for insights tasks: %w", err)
	}
	i.logger.Infof("Successfully connected with %s channel", sub.Subject)

	i.agentTaskSub = sub
	return nil
}

// subscribeToSummarizeJobs sets up a durable consumer to handle summarization jobs from the JetStream.
func (i *InsightsController) subscribeToSummarizeJobs(ctx context.Context) error {
	consumer, err := i.natsService.CreateSummarizeJobStreamWithConsumer(ctx, i.logger)
	if err != nil {
		return err
	}

	consumeCtx, err := consumer.Consume(func(msg jetstream.Msg) {
		var payload insights.SummarizeJobPayload
		if err := json.Unmarshal(msg.Data(), &payload); err != nil {
			i.logger.WithError(err).Error("failed to unmarshal summarize job payload")
			if err := msg.Nak(); err != nil {
				i.logger.WithError(err).Error("failed to send NAK")
			}
			return
		}

		metadata, err := msg.Metadata()
		if err != nil {
			i.logger.WithError(err).Error("failed to get msg metadata")
			if err := msg.Nak(); err != nil {
				i.logger.WithError(err).Error("failed to send NAK")
			}
			return
		}

		// Pass the payload to a new model method for processing.
		if err := i.insightsModel.StartProcessingSummarizeJob(&payload, metadata); err != nil {
			i.logger.WithError(err).Error("failed to process summarize job")
			if err := msg.NakWithDelay(time.Minute * 5); err != nil {
				i.logger.WithError(err).Error("failed to send NAK with delay")
			}
			return
		}

		if err := msg.Ack(); err != nil {
			i.logger.WithError(err).Error("failed to send ACK")
		}
	}, jetstream.ConsumeErrHandler(func(consumeCtx jetstream.ConsumeContext, err error) {
		if ctx.Err() == nil {
			if !errors.Is(err, jetstream.ErrConnectionClosed) {
				i.logger.WithError(err).Warn("jetstream consume error for summarization jobs")
			}
		}
	}))

	if err != nil {
		return fmt.Errorf("failed to subscribe to NATS for summarize jobs: %w", err)
	}

	i.logger.Infof("Successfully connected with %s queue", insights.SummarizeJobQueueSubject)
	i.summarizeJobSub = consumeCtx
	return nil
}

func (i *InsightsController) Shutdown() {
	if i.agentTaskSub != nil {
		if err := i.agentTaskSub.Unsubscribe(); err != nil {
			i.logger.WithError(err).Errorln("failed to unsubscribe from agent tasks")
		}
	}
	if i.summarizeJobSub != nil {
		i.summarizeJobSub.Stop()
	}
	i.insightsModel.Shutdown()
}

func (i *InsightsController) HandleTranscriptionConfigure(c fiber.Ctx) error {
	if i.app.Insights == nil || !i.app.Insights.Enabled {
		return utils.SendCommonProtobufResponse(c, false, "insights feature wasn't configured")
	}
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	roomId := fiber.Locals[string](c, "roomId")

	if !isAdmin {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	req := new(plugnmeet.InsightsTranscriptionConfigReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	if err := i.insightsModel.TranscriptionConfigure(req, roomId); err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

func (i *InsightsController) HandleTranscriptionUserSession(c fiber.Ctx) error {
	roomId := fiber.Locals[string](c, "roomId")
	requestedUserId := fiber.Locals[string](c, "requestedUserId")

	req := new(plugnmeet.InsightsTranscriptionUserSessionReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	if err := i.insightsModel.TranscriptionUserSession(req, roomId, requestedUserId); err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

func (i *InsightsController) HandleEndTranscription(c fiber.Ctx) error {
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	roomId := fiber.Locals[string](c, "roomId")

	if !isAdmin {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	if err := i.insightsModel.EndTranscription(roomId); err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

func (i *InsightsController) HandleGetTranscriptionUserTaskStatus(c fiber.Ctx) error {
	roomId := fiber.Locals[string](c, "roomId")
	requestedUserId := fiber.Locals[string](c, "requestedUserId")

	res, err := i.insightsModel.GetUserTaskStatus(insights.ServiceTypeTranscription, roomId, requestedUserId, time.Second*5)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	c.Set("Content-Type", "application/protobuf")
	return c.Send(res)
}

func (i *InsightsController) HandleGetSupportedLangs(c fiber.Ctx) error {
	req := new(plugnmeet.InsightsGetSupportedLanguagesReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	serviceType, err := insights.ToServiceType(req.ServiceType)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	langs, err := i.insightsModel.GetSupportedLanguagesForService(c.RequestCtx(), serviceType)
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

func (i *InsightsController) HandleChatTranslationConfigure(c fiber.Ctx) error {
	if i.app.Insights == nil || !i.app.Insights.Enabled {
		return utils.SendCommonProtobufResponse(c, false, "insights feature wasn't configured")
	}
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	roomId := fiber.Locals[string](c, "roomId")

	if !isAdmin {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	req := new(plugnmeet.InsightsChatTranslationConfigReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	if err := i.insightsModel.ChatTranslationConfigure(req, roomId); err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

func (i *InsightsController) HandleExecuteChatTranslation(c fiber.Ctx) error {
	roomId := fiber.Locals[string](c, "roomId")
	requestedUserId := fiber.Locals[string](c, "requestedUserId")

	req := new(plugnmeet.InsightsTranslateTextReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	result, err := i.insightsModel.ExecuteChatTranslation(c.RequestCtx(), req, roomId, requestedUserId)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendProtobufResponse(c, result)
}

func (i *InsightsController) HandleEndChatTranslation(c fiber.Ctx) error {
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	roomId := fiber.Locals[string](c, "roomId")

	if !isAdmin {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	if err := i.insightsModel.ChatEndTranslation(roomId); err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}
	return utils.SendCommonProtobufResponse(c, true, "success")
}

func (i *InsightsController) HandleAITextChatConfigure(c fiber.Ctx) error {
	if i.app.Insights == nil || !i.app.Insights.Enabled {
		return utils.SendCommonProtobufResponse(c, false, "insights feature wasn't configured")
	}
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	roomId := fiber.Locals[string](c, "roomId")

	if !isAdmin {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	req := new(plugnmeet.InsightsAITextChatConfigReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	if err := i.insightsModel.AITextChatConfigure(req, roomId); err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

func (i *InsightsController) HandleExecuteAITextChat(c fiber.Ctx) error {
	roomId := fiber.Locals[string](c, "roomId")
	requestedUserId := fiber.Locals[string](c, "requestedUserId")

	req := new(plugnmeet.InsightsAITextChatContent)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	if err := i.insightsModel.ExecuteAITextChat(req, roomId, requestedUserId); err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

func (i *InsightsController) HandleEndAITextChat(c fiber.Ctx) error {
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	roomId := fiber.Locals[string](c, "roomId")

	if !isAdmin {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	if err := i.insightsModel.EndAITextChat(roomId); err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}
	return utils.SendCommonProtobufResponse(c, true, "success")
}

func (i *InsightsController) HandleAIMeetingSummarizationConfig(c fiber.Ctx) error {
	if i.app.Insights == nil || !i.app.Insights.Enabled {
		return utils.SendCommonProtobufResponse(c, false, "insights feature wasn't configured")
	}
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	roomId := fiber.Locals[string](c, "roomId")

	if !isAdmin {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	req := new(plugnmeet.InsightsAIMeetingSummarizationConfigReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	if err := i.insightsModel.AIMeetingSummarizationConfig(req, roomId); err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

func (i *InsightsController) HandleEndAIMeetingSummarization(c fiber.Ctx) error {
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	roomId := fiber.Locals[string](c, "roomId")

	if !isAdmin {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	if err := i.insightsModel.EndEndAIMeetingSummarization(roomId); err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}
	return utils.SendCommonProtobufResponse(c, true, "success")
}
