package controllers

import (
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"google.golang.org/protobuf/proto"
)

// PollsController holds dependencies for poll-related handlers.
type PollsController struct {
	PollModel    *models.PollModel
	RedisService *redisservice.RedisService
}

// NewPollsController creates a new PollsController.
func NewPollsController(pm *models.PollModel, rs *redisservice.RedisService) *PollsController {
	return &PollsController{
		PollModel:    pm,
		RedisService: rs,
	}
}

// HandleActivatePolls handles activating or deactivating polls.
func (pc *PollsController) HandleActivatePolls(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	isAdmin := c.Locals("isAdmin")

	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	rid := roomId.(string)
	if rid == "" {
		return utils.SendCommonProtobufResponse(c, false, "roomId required")
	}

	req := new(plugnmeet.ActivatePollsReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}
	req.RoomId = roomId.(string)
	err = pc.PollModel.ManageActivation(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}
	return utils.SendCommonProtobufResponse(c, true, "success")
}

// HandleCreatePoll handles creating a new poll.
func (pc *PollsController) HandleCreatePoll(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	isAdmin := c.Locals("isAdmin")
	requestedUserId := c.Locals("requestedUserId")
	res := new(plugnmeet.PollResponse)
	res.Status = false

	if !isAdmin.(bool) {
		res.Msg = "only admin can perform this task"
		return sendPollResponse(c, res)
	}

	req := new(plugnmeet.CreatePollReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		res.Msg = err.Error()
		return sendPollResponse(c, res)
	}

	req.RoomId = roomId.(string)
	req.UserId = requestedUserId.(string)
	pollId, err := pc.PollModel.CreatePoll(req)
	if err != nil {
		res.Msg = err.Error()
		return sendPollResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.PollId = &pollId
	return sendPollResponse(c, res)
}

// HandleListPolls lists all polls for a room.
func (pc *PollsController) HandleListPolls(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	res := new(plugnmeet.PollResponse)
	res.Status = false

	polls, err := pc.PollModel.ListPolls(roomId.(string))
	if err != nil {
		res.Msg = err.Error()
		return sendPollResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.Polls = polls
	return sendPollResponse(c, res)
}

// HandleCountPollTotalResponses counts the total responses for a poll.
func (pc *PollsController) HandleCountPollTotalResponses(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	pollId := c.Params("pollId")
	res := new(plugnmeet.PollResponse)
	res.Status = false

	if pollId == "" {
		res.Msg = "pollId required"
		return sendPollResponse(c, res)
	}

	responses, err := pc.RedisService.GetPollTotalResponses(roomId.(string), pollId)
	if err != nil {
		res.Msg = err.Error()
		return sendPollResponse(c, res)
	}

	rps, err := strconv.ParseUint(responses, 10, 64)
	if err != nil {
		res.Msg = err.Error()
		return sendPollResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.PollId = &pollId
	res.TotalResponses = &rps
	return sendPollResponse(c, res)
}

// HandleUserSelectedOption checks which option a user selected.
func (pc *PollsController) HandleUserSelectedOption(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	pollId := c.Params("pollId")
	userId := c.Params("userId")
	res := new(plugnmeet.PollResponse)
	res.Status = false

	if pollId == "" || userId == "" {
		res.Msg = "both userId & pollId required"
		return sendPollResponse(c, res)
	}

	voted, _ := pc.PollModel.UserSelectedOption(roomId.(string), pollId, userId)

	res.Status = true
	res.Msg = "success"
	res.PollId = &pollId
	res.Voted = &voted
	return sendPollResponse(c, res)
}

// HandleUserSubmitResponse handles a user's poll submission.
func (pc *PollsController) HandleUserSubmitResponse(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	res := new(plugnmeet.PollResponse)
	res.Status = false

	req := new(plugnmeet.SubmitPollResponseReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		res.Msg = err.Error()
		return sendPollResponse(c, res)
	}

	req.RoomId = roomId.(string)
	err = pc.PollModel.UserSubmitResponse(req)
	if err != nil {
		res.Msg = err.Error()
		return sendPollResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.PollId = &req.PollId
	return sendPollResponse(c, res)
}

// HandleClosePoll handles closing a poll.
func (pc *PollsController) HandleClosePoll(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	isAdmin := c.Locals("isAdmin")
	requestedUserId := c.Locals("requestedUserId")
	res := new(plugnmeet.PollResponse)
	res.Status = false

	if !isAdmin.(bool) {
		res.Msg = "only admin can perform this task"
		return sendPollResponse(c, res)
	}

	req := new(plugnmeet.ClosePollReq)

	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		res.Msg = err.Error()
		return sendPollResponse(c, res)
	}

	req.RoomId = roomId.(string)
	req.UserId = requestedUserId.(string)
	err = pc.PollModel.ClosePoll(req)
	if err != nil {
		res.Msg = err.Error()
		return sendPollResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.PollId = &req.PollId
	return sendPollResponse(c, res)
}

// HandleGetPollResponsesDetails gets detailed responses for a poll.
func (pc *PollsController) HandleGetPollResponsesDetails(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	pollId := c.Params("pollId")
	isAdmin := c.Locals("isAdmin")
	res := new(plugnmeet.PollResponse)
	res.Status = false

	if !isAdmin.(bool) {
		res.Msg = "only admin can perform this task"
		return sendPollResponse(c, res)
	}

	if pollId == "" {
		res.Msg = "pollId required"
		return sendPollResponse(c, res)
	}

	responses, err := pc.PollModel.GetPollResponsesDetails(roomId.(string), pollId)
	if err != nil {
		res.Msg = err.Error()
		return sendPollResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.PollId = &pollId
	res.Responses = responses
	return sendPollResponse(c, res)
}

// HandleGetResponsesResult gets the aggregated results of a poll.
func (pc *PollsController) HandleGetResponsesResult(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	pollId := c.Params("pollId")
	res := new(plugnmeet.PollResponse)
	res.Status = false

	result, err := pc.PollModel.GetResponsesResult(roomId.(string), pollId)
	if err != nil {
		res.Msg = err.Error()
		return sendPollResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.PollId = &pollId
	res.PollResponsesResult = result
	return sendPollResponse(c, res)
}

// HandleGetPollsStats gets statistics for all polls in a room.
func (pc *PollsController) HandleGetPollsStats(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	res := new(plugnmeet.PollResponse)
	res.Status = false

	stats, err := pc.PollModel.GetPollsStats(roomId.(string))
	if err != nil {
		res.Msg = err.Error()
		return sendPollResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.Stats = stats
	return sendPollResponse(c, res)
}

func sendPollResponse(c *fiber.Ctx, res *plugnmeet.PollResponse) error {
	marshal, err := proto.Marshal(res)
	if err != nil {
		return err
	}
	c.Set("Content-Type", "application/protobuf")
	return c.Send(marshal)
}
