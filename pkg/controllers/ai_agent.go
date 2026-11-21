package controllers

import (
	"context"

	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
	"google.golang.org/protobuf/proto"
)

// AIAgentController exposes the moderator and host controls of the AI agent.
type AIAgentController struct {
	aiAgentModel *models.AIAgentModel
	logger       *logrus.Entry
}

type AIAgentControllerArgs struct {
	fx.In
	AIAgentModel *models.AIAgentModel
	Logger       *logrus.Logger
}

func NewAIAgentController(args AIAgentControllerArgs) *AIAgentController {
	return &AIAgentController{
		aiAgentModel: args.AIAgentModel,
		logger:       args.Logger.WithField("controller", "aiAgent"),
	}
}

func (a *AIAgentController) roomContext(c fiber.Ctx) (context.Context, string, string, bool) {
	return c.Context(), fiber.Locals[string](c, "roomId"), fiber.Locals[string](c, "requestedUserId"), fiber.Locals[bool](c, "isAdmin")
}

// canControlSession allows an admin, the current delegate and the room's
// pre-authorized participants to drive the agent session.
func (a *AIAgentController) canControlSession(ctx context.Context, roomId, userId string, isAdmin bool) bool {
	if isAdmin {
		return true
	}

	return a.aiAgentModel.CanControlSession(ctx, roomId, userId)
}

// HandleStart activates the agent for the room, skipping the auto-join rule.
func (a *AIAgentController) HandleStart(c fiber.Ctx) error {
	ctx, roomId, userId, isAdmin := a.roomContext(c)
	res := new(plugnmeet.AIAgentStatusRes)
	if !a.canControlSession(ctx, roomId, userId, isAdmin) {
		res.Msg = config.ErrAIAgentNotAllowed.Error()
		return utils.SendProtobufResponse(c, res)
	}

	req := new(plugnmeet.AIAgentStartReq)
	if len(c.Body()) > 0 {
		if err := proto.Unmarshal(c.Body(), req); err != nil {
			a.logger.WithError(err).WithField("roomId", roomId).Warnln("failed to unmarshal the ai agent start request")
			res.Msg = config.ErrAIAgentInvalidRequest.Error()
			return utils.SendProtobufResponse(c, res)
		}
	}

	if err := a.aiAgentModel.Start(ctx, roomId, req); err != nil {
		a.logger.WithError(err).WithField("roomId", roomId).Warnln("failed to start the ai agent")
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	return a.sendStatus(ctx, c, roomId)
}

// HandleStop ends the session now.
func (a *AIAgentController) HandleStop(c fiber.Ctx) error {
	ctx, roomId, userId, isAdmin := a.roomContext(c)
	res := new(plugnmeet.AIAgentStatusRes)
	if !a.canControlSession(ctx, roomId, userId, isAdmin) {
		res.Msg = config.ErrAIAgentNotAllowed.Error()
		return utils.SendProtobufResponse(c, res)
	}

	reason := "host_stopped"
	if !isAdmin {
		reason = "user_stopped"
	}
	req := new(plugnmeet.AIAgentStopReq)
	if len(c.Body()) > 0 {
		if err := proto.Unmarshal(c.Body(), req); err == nil && req.GetReason() != "" {
			reason = req.GetReason()
		}
	}

	if err := a.aiAgentModel.Stop(ctx, roomId, reason); err != nil {
		a.logger.WithError(err).WithField("roomId", roomId).Warnln("failed to stop the ai agent")
		res.Msg = config.ErrAIAgentGeneric.Error()
		return utils.SendProtobufResponse(c, res)
	}

	// The session state is already gone, so report the stop itself.
	return utils.SendProtobufResponse(c, &plugnmeet.AIAgentStatusRes{
		Status: true,
		Msg:    config.MsgSuccess,
		State: &plugnmeet.AIAgentStateMsg{
			State:  plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_STOPPED,
			Reason: reason,
		},
	})
}

// HandleStatus reports the current agent state.
func (a *AIAgentController) HandleStatus(c fiber.Ctx) error {
	ctx, roomId, _, _ := a.roomContext(c)
	return a.sendStatus(ctx, c, roomId)
}

// HandleSendContext injects a host-side event into the live session.
func (a *AIAgentController) HandleSendContext(c fiber.Ctx) error {
	ctx, roomId, userId, isAdmin := a.roomContext(c)
	res := new(plugnmeet.AIAgentStatusRes)
	if !a.canControlSession(ctx, roomId, userId, isAdmin) {
		res.Msg = config.ErrAIAgentOnlyAdmin.Error()
		return utils.SendProtobufResponse(c, res)
	}

	req := new(plugnmeet.AIAgentSendContextReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		a.logger.WithError(err).WithField("roomId", roomId).Warnln("failed to unmarshal the ai agent send context request")
		res.Msg = config.ErrAIAgentInvalidRequest.Error()
		return utils.SendProtobufResponse(c, res)
	}

	if err := a.aiAgentModel.SendContext(ctx, roomId, req); err != nil {
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	return a.sendStatus(ctx, c, roomId)
}

// HandlePause stops the agent's audio but keeps the provider session open.
func (a *AIAgentController) HandlePause(c fiber.Ctx) error {
	ctx, roomId, userId, isAdmin := a.roomContext(c)
	res := new(plugnmeet.AIAgentStatusRes)
	if !a.canControlSession(ctx, roomId, userId, isAdmin) {
		res.Msg = config.ErrAIAgentOnlyAdmin.Error()
		return utils.SendProtobufResponse(c, res)
	}

	if err := a.aiAgentModel.Pause(ctx, roomId); err != nil {
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	return a.sendStatus(ctx, c, roomId)
}

// HandleResume re-enables the agent's audio.
func (a *AIAgentController) HandleResume(c fiber.Ctx) error {
	ctx, roomId, userId, isAdmin := a.roomContext(c)
	res := new(plugnmeet.AIAgentStatusRes)
	if !a.canControlSession(ctx, roomId, userId, isAdmin) {
		res.Msg = config.ErrAIAgentOnlyAdmin.Error()
		return utils.SendProtobufResponse(c, res)
	}

	if err := a.aiAgentModel.Resume(ctx, roomId); err != nil {
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	return a.sendStatus(ctx, c, roomId)
}

// HandleRedelegate forces delegate re-selection.
func (a *AIAgentController) HandleRedelegate(c fiber.Ctx) error {
	ctx, roomId, _, isAdmin := a.roomContext(c)
	res := new(plugnmeet.AIAgentStatusRes)
	if !isAdmin {
		res.Msg = config.ErrAIAgentOnlyAdmin.Error()
		return utils.SendProtobufResponse(c, res)
	}

	req := new(plugnmeet.AIAgentRedelegateReq)
	if len(c.Body()) > 0 {
		if err := proto.Unmarshal(c.Body(), req); err != nil {
			res.Msg = config.ErrAIAgentInvalidRequest.Error()
			return utils.SendProtobufResponse(c, res)
		}
	}

	if _, err := a.aiAgentModel.Redelegate(ctx, roomId, req); err != nil {
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	return a.sendStatus(ctx, c, roomId)
}

func (a *AIAgentController) sendStatus(ctx context.Context, c fiber.Ctx, roomId string) error {
	res, err := a.aiAgentModel.Status(ctx, roomId)
	if err != nil {
		a.logger.WithError(err).WithField("roomId", roomId).Warnln("failed to read the ai agent status")
		return utils.SendProtobufResponse(c, &plugnmeet.AIAgentStatusRes{Msg: config.ErrAIAgentGeneric.Error()})
	}

	return utils.SendProtobufResponse(c, res)
}
