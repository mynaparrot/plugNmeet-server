package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"google.golang.org/protobuf/proto"
	"strconv"
)

func HandleCreatePoll(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	isAdmin := c.Locals("isAdmin")
	requestedUserId := c.Locals("requestedUserId")
	res := new(plugnmeet.PollResponse)
	res.Status = false

	if !isAdmin.(bool) {
		res.Msg = "only admin can perform this task"
		return SendPollResponse(c, res)
	}

	req := new(plugnmeet.CreatePollReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		res.Msg = err.Error()
		return SendPollResponse(c, res)
	}

	req.RoomId = roomId.(string)
	req.UserId = requestedUserId.(string)
	m := models.NewPollsModel()
	err, pollId := m.CreatePoll(req, isAdmin.(bool))
	if err != nil {
		res.Msg = err.Error()
		return SendPollResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.PollId = &pollId
	return SendPollResponse(c, res)
}

func HandleListPolls(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	res := new(plugnmeet.PollResponse)
	res.Status = false

	m := models.NewPollsModel()
	err, polls := m.ListPolls(roomId.(string))
	if err != nil {
		res.Msg = err.Error()
		return SendPollResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.Polls = polls
	return SendPollResponse(c, res)
}

func HandleCountPollTotalResponses(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	pollId := c.Params("pollId")
	res := new(plugnmeet.PollResponse)
	res.Status = false

	if pollId == "" {
		res.Msg = "pollId required"
		return SendPollResponse(c, res)
	}

	m := models.NewPollsModel()
	err, responses := m.GetPollResponsesByField(roomId.(string), pollId, "total_resp")

	if err != nil {
		res.Msg = err.Error()
		return SendPollResponse(c, res)
	}

	rps, err := strconv.ParseUint(responses, 10, 64)
	if err != nil {
		res.Msg = err.Error()
		return SendPollResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.PollId = &pollId
	res.TotalResponses = &rps
	return SendPollResponse(c, res)
}

func HandleUserSelectedOption(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	pollId := c.Params("pollId")
	userId := c.Params("userId")
	res := new(plugnmeet.PollResponse)
	res.Status = false

	if pollId == "" || userId == "" {
		res.Msg = "both userId & pollId required"
		return SendPollResponse(c, res)
	}

	m := models.NewPollsModel()
	voted, _ := m.UserSelectedOption(roomId.(string), pollId, userId)

	res.Status = true
	res.Msg = "success"
	res.PollId = &pollId
	res.Voted = &voted
	return SendPollResponse(c, res)
}

func HandleUserSubmitResponse(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	isAdmin := c.Locals("isAdmin")
	res := new(plugnmeet.PollResponse)
	res.Status = false

	req := new(plugnmeet.SubmitPollResponseReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		res.Msg = err.Error()
		return SendPollResponse(c, res)
	}

	req.RoomId = roomId.(string)
	m := models.NewPollsModel()
	err = m.UserSubmitResponse(req, isAdmin.(bool))
	if err != nil {
		res.Msg = err.Error()
		return SendPollResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.PollId = &req.PollId
	return SendPollResponse(c, res)
}

func HandleClosePoll(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	isAdmin := c.Locals("isAdmin")
	requestedUserId := c.Locals("requestedUserId")
	res := new(plugnmeet.PollResponse)
	res.Status = false

	if !isAdmin.(bool) {
		res.Msg = "only admin can perform this task"
		return SendPollResponse(c, res)
	}

	m := models.NewPollsModel()
	req := new(plugnmeet.ClosePollReq)

	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		res.Msg = err.Error()
		return SendPollResponse(c, res)
	}

	req.RoomId = roomId.(string)
	req.UserId = requestedUserId.(string)
	err = m.ClosePoll(req, isAdmin.(bool))
	if err != nil {
		res.Msg = err.Error()
		return SendPollResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.PollId = &req.PollId
	return SendPollResponse(c, res)
}

func HandleGetPollResponsesDetails(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	pollId := c.Params("pollId")
	isAdmin := c.Locals("isAdmin")
	res := new(plugnmeet.PollResponse)
	res.Status = false

	if !isAdmin.(bool) {
		res.Msg = "only admin can perform this task"
		return SendPollResponse(c, res)
	}

	if pollId == "" {
		res.Msg = "pollId required"
		return SendPollResponse(c, res)
	}

	m := models.NewPollsModel()
	err, responses := m.GetPollResponsesDetails(roomId.(string), pollId)
	if err != nil {
		res.Msg = err.Error()
		return SendPollResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.PollId = &pollId
	res.Responses = responses
	return SendPollResponse(c, res)
}

func HandleGetResponsesResult(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	pollId := c.Params("pollId")
	res := new(plugnmeet.PollResponse)
	res.Status = false

	m := models.NewPollsModel()
	result, err := m.GetResponsesResult(roomId.(string), pollId)
	if err != nil {
		res.Msg = err.Error()
		return SendPollResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.PollId = &pollId
	res.PollResponsesResult = result
	return SendPollResponse(c, res)
}

func HandleGetPollsStats(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	res := new(plugnmeet.PollResponse)
	res.Status = false

	m := models.NewPollsModel()
	stats, err := m.GetPollsStats(roomId.(string))
	if err != nil {
		res.Msg = err.Error()
		return SendPollResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.Stats = stats
	return SendPollResponse(c, res)
}

func SendPollResponse(c *fiber.Ctx, res *plugnmeet.PollResponse) error {
	marshal, err := proto.Marshal(res)
	if err != nil {
		return err
	}
	c.Set("Content-Type", "application/protobuf")
	return c.Send(marshal)
}
