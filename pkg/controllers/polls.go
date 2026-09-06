package controllers

import (
	"strconv"

	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
	"google.golang.org/protobuf/proto"
)

// PollsController holds dependencies for poll-related handlers.
type PollsController struct {
	PollModel    *models.PollModel
	RoomModel    *models.RoomModel
	RedisService *redisservice.RedisService
	logger       *logrus.Entry
}

type PollsControllerArgs struct {
	fx.In
	PollModel    *models.PollModel
	RoomModel    *models.RoomModel
	RedisService *redisservice.RedisService
	Logger       *logrus.Logger
}

// NewPollsController creates a new PollsController.
func NewPollsController(args PollsControllerArgs) *PollsController {
	return &PollsController{
		PollModel:    args.PollModel,
		RoomModel:    args.RoomModel,
		RedisService: args.RedisService,
		logger:       args.Logger.WithField("controller", "polls"),
	}
}

// HandleCreatePoll handles creating a new poll.
func (pc *PollsController) HandleCreatePoll(c fiber.Ctx) error {
	roomId := fiber.Locals[string](c, "roomId")
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	requestedUserId := fiber.Locals[string](c, "requestedUserId")
	res := new(plugnmeet.PollResponse)
	res.Status = false

	if !isAdmin {
		res.Msg = config.ErrPollOnlyAdmin.Error()
		return utils.SendProtobufResponse(c, res)
	}

	req := new(plugnmeet.CreatePollReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		pc.logger.WithError(err).WithField("roomId", roomId).Warnln("failed to unmarshal create poll request")
		res.Msg = config.ErrPollGeneric.Error()
		return utils.SendProtobufResponse(c, res)
	}

	req.RoomId = roomId
	req.UserId = requestedUserId
	pollId, err := pc.PollModel.CreatePoll(req)
	if err != nil {
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.PollId = &pollId
	return utils.SendProtobufResponse(c, res)
}

// HandleCreatePollAuth creates a poll pushed by a trusted external app via the
// Auth API; the room must already be running.
func (pc *PollsController) HandleCreatePollAuth(c fiber.Ctx) error {
	req := new(plugnmeet.CreatePollReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		pc.logger.WithError(err).Warnln("failed to unmarshal create poll request")
		return utils.SendCommonProtoJsonResponse(c, false, err.Error(), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	if req.RoomId == "" {
		return utils.SendCommonProtoJsonResponse(c, false, config.MsgRoomIdRequired, plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	// same running-session check as HandleUploadWhiteboardFile
	res, _, _ := pc.RoomModel.IsRoomActive(&plugnmeet.IsRoomActiveReq{RoomId: req.RoomId})
	if !res.GetIsActive() {
		return utils.SendCommonProtoJsonResponse(c, false, res.GetMsg(), res.GetStatusCode())
	}

	if req.UserId == "" {
		req.UserId = config.ExternalApiUserId
	}

	pollId, err := pc.PollModel.CreatePoll(req)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error(), plugnmeet.StatusCode_INTERNAL_SERVER_ERROR)
	}

	return utils.SendProtoJsonResponse(c, &plugnmeet.CreatePollRes{
		Status: true,
		Msg:    "success",
		PollId: pollId,
	})
}

// HandleActivatePolls handles activating or deactivating polls.
func (pc *PollsController) HandleActivatePolls(c fiber.Ctx) error {
	roomId := fiber.Locals[string](c, "roomId")
	isAdmin := fiber.Locals[bool](c, "isAdmin")

	if !isAdmin {
		return utils.SendCommonProtobufResponse(c, false, config.ErrPollOnlyAdmin.Error())
	}

	rid := roomId
	if rid == "" {
		return utils.SendCommonProtobufResponse(c, false, config.ErrPollRoomIdRequired.Error())
	}

	req := new(plugnmeet.ActivatePollsReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		pc.logger.WithError(err).WithField("roomId", roomId).Warnln("failed to unmarshal activate polls request")
		return utils.SendCommonProtobufResponse(c, false, config.ErrPollGeneric.Error())
	}
	req.RoomId = roomId
	err = pc.PollModel.ManageActivation(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}
	return utils.SendCommonProtobufResponse(c, true, "success")
}

// HandleListPolls lists all polls for a room.
func (pc *PollsController) HandleListPolls(c fiber.Ctx) error {
	roomId := fiber.Locals[string](c, "roomId")
	res := new(plugnmeet.PollResponse)
	res.Status = false

	polls, err := pc.PollModel.ListPolls(roomId)
	if err != nil {
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.Polls = polls
	return utils.SendProtobufResponse(c, res)
}

// HandleCountPollTotalResponses counts the total responses for a poll.
func (pc *PollsController) HandleCountPollTotalResponses(c fiber.Ctx) error {
	roomId := fiber.Locals[string](c, "roomId")
	pollId := c.Params("pollId")
	res := new(plugnmeet.PollResponse)
	res.Status = false

	if pollId == "" {
		res.Msg = config.ErrPollPollIdRequired.Error()
		return utils.SendProtobufResponse(c, res)
	}

	responses, err := pc.RedisService.GetPollTotalResponses(roomId, pollId)
	if err != nil {
		pc.logger.WithError(err).WithFields(logrus.Fields{
			"roomId": roomId,
			"pollId": pollId,
		}).Errorln("failed to fetch poll total responses from redis")
		res.Msg = config.ErrPollGeneric.Error()
		return utils.SendProtobufResponse(c, res)
	}

	rps, err := strconv.ParseUint(responses, 10, 64)
	if err != nil {
		pc.logger.WithError(err).WithFields(logrus.Fields{
			"roomId":   roomId,
			"pollId":   pollId,
			"response": responses,
		}).Errorln("failed to parse poll total responses counter")
		res.Msg = config.ErrPollGeneric.Error()
		return utils.SendProtobufResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.PollId = &pollId
	res.TotalResponses = &rps
	return utils.SendProtobufResponse(c, res)
}

// HandleUserSelectedOption checks which option a user selected.
func (pc *PollsController) HandleUserSelectedOption(c fiber.Ctx) error {
	roomId := fiber.Locals[string](c, "roomId")
	pollId := c.Params("pollId")
	userId := c.Params("userId")
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	requestedUserId := fiber.Locals[string](c, "requestedUserId")
	res := new(plugnmeet.PollResponse)
	res.Status = false

	if pollId == "" || userId == "" {
		res.Msg = config.ErrPollUserAndPollIdRequired.Error()
		return utils.SendProtobufResponse(c, res)
	}

	if !isAdmin && userId != requestedUserId {
		res.Msg = config.ErrPollOnlyAdminViewSelection.Error()
		return utils.SendProtobufResponse(c, res)
	}

	voted, hasVoted, err := pc.PollModel.UserSelectedOption(roomId, pollId, userId)
	if err != nil {
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.PollId = &pollId
	res.Voted = voted
	res.HasVoted = hasVoted
	return utils.SendProtobufResponse(c, res)
}

// HandleUserSubmitResponse handles a user's poll submission.
func (pc *PollsController) HandleUserSubmitResponse(c fiber.Ctx) error {
	roomId := fiber.Locals[string](c, "roomId")
	res := new(plugnmeet.PollResponse)
	res.Status = false

	req := new(plugnmeet.SubmitPollResponseReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		pc.logger.WithError(err).WithField("roomId", roomId).Warnln("failed to unmarshal submit poll response request")
		res.Msg = config.ErrPollGeneric.Error()
		return utils.SendProtobufResponse(c, res)
	}

	req.RoomId = roomId
	err = pc.PollModel.UserSubmitResponse(req)
	if err != nil {
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.PollId = &req.PollId
	return utils.SendProtobufResponse(c, res)
}

// HandleClosePoll handles closing a poll.
func (pc *PollsController) HandleClosePoll(c fiber.Ctx) error {
	roomId := fiber.Locals[string](c, "roomId")
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	requestedUserId := fiber.Locals[string](c, "requestedUserId")
	res := new(plugnmeet.PollResponse)
	res.Status = false

	if !isAdmin {
		res.Msg = config.ErrPollOnlyAdmin.Error()
		return utils.SendProtobufResponse(c, res)
	}

	req := new(plugnmeet.ClosePollReq)

	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		pc.logger.WithError(err).WithField("roomId", roomId).Warnln("failed to unmarshal close poll request")
		res.Msg = config.ErrPollGeneric.Error()
		return utils.SendProtobufResponse(c, res)
	}

	req.RoomId = roomId
	req.UserId = requestedUserId
	err = pc.PollModel.ClosePoll(req)
	if err != nil {
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.PollId = &req.PollId
	return utils.SendProtobufResponse(c, res)
}

// HandleReopenPoll handles reopening a closed poll.
func (pc *PollsController) HandleReopenPoll(c fiber.Ctx) error {
	roomId := fiber.Locals[string](c, "roomId")
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	requestedUserId := fiber.Locals[string](c, "requestedUserId")
	res := new(plugnmeet.PollResponse)
	res.Status = false

	if !isAdmin {
		res.Msg = config.ErrPollOnlyAdmin.Error()
		return utils.SendProtobufResponse(c, res)
	}

	req := new(plugnmeet.ReopenPollReq)

	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		pc.logger.WithError(err).WithField("roomId", roomId).Warnln("failed to unmarshal reopen poll request")
		res.Msg = config.ErrPollGeneric.Error()
		return utils.SendProtobufResponse(c, res)
	}

	req.RoomId = roomId
	req.UserId = requestedUserId
	err = pc.PollModel.ReopenPoll(req)
	if err != nil {
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.PollId = &req.PollId
	return utils.SendProtobufResponse(c, res)
}

// HandleGetPollResponsesDetails gets detailed responses for a poll.
func (pc *PollsController) HandleGetPollResponsesDetails(c fiber.Ctx) error {
	roomId := fiber.Locals[string](c, "roomId")
	pollId := c.Params("pollId")
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	res := new(plugnmeet.PollResponse)
	res.Status = false

	if !isAdmin {
		res.Msg = config.ErrPollOnlyAdmin.Error()
		return utils.SendProtobufResponse(c, res)
	}

	if pollId == "" {
		res.Msg = config.ErrPollPollIdRequired.Error()
		return utils.SendProtobufResponse(c, res)
	}

	responses, err := pc.PollModel.GetPollResponsesDetails(roomId, pollId)
	if err != nil {
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.PollId = &pollId
	res.Responses = responses
	return utils.SendProtobufResponse(c, res)
}

// HandleGetResponsesResult gets the aggregated results of a poll.
func (pc *PollsController) HandleGetResponsesResult(c fiber.Ctx) error {
	roomId := fiber.Locals[string](c, "roomId")
	pollId := c.Params("pollId")
	res := new(plugnmeet.PollResponse)
	res.Status = false

	result, err := pc.PollModel.GetResponsesResult(roomId, pollId)
	if err != nil {
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.PollId = &pollId
	res.PollResponsesResult = result
	return utils.SendProtobufResponse(c, res)
}

// HandleGetPollsStats gets statistics for all polls in a room.
func (pc *PollsController) HandleGetPollsStats(c fiber.Ctx) error {
	roomId := fiber.Locals[string](c, "roomId")
	res := new(plugnmeet.PollResponse)
	res.Status = false

	stats, err := pc.PollModel.GetPollsStats(roomId)
	if err != nil {
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.Stats = stats
	return utils.SendProtobufResponse(c, res)
}
