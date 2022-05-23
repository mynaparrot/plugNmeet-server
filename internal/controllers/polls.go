package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugNmeet/internal/config"
	"github.com/mynaparrot/plugNmeet/internal/models"
)

func HandleCreatePoll(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	isAdmin := c.Locals("isAdmin")
	requestedUserId := c.Locals("requestedUserId")

	if !isAdmin.(bool) {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "only admin can perform this task",
		})
	}

	m := models.NewPollsModel()
	req := new(models.CreatePollReq)

	err := c.BodyParser(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	check := config.AppCnf.DoValidateReq(req)
	if len(check) > 0 {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    check,
		})
	}

	req.RoomId = roomId.(string)
	req.UserId = requestedUserId.(string)
	err, pollId := m.CreatePoll(req, isAdmin.(bool))
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"status":  true,
		"msg":     "success",
		"poll_id": pollId,
	})
}

func HandleListPolls(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")

	m := models.NewPollsModel()
	err, polls := m.ListPolls(roomId.(string))
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"status": true,
		"msg":    "success",
		"polls":  polls,
	})
}

func HandleCountPollTotalResponses(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	pollId := c.Params("pollId")

	if pollId == "" {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "pollId required",
		})
	}

	m := models.NewPollsModel()
	err, responses := m.GetPollResponsesByField(roomId.(string), pollId, "total_resp")

	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"status":          true,
		"msg":             "success",
		"poll_id":         pollId,
		"total_responses": responses,
	})
}

func HandleUserSelectedOption(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	pollId := c.Params("pollId")
	userId := c.Params("userId")

	if pollId == "" || userId == "" {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "both userId & pollId required",
		})
	}

	m := models.NewPollsModel()
	err, voted := m.UserSelectedOption(roomId.(string), pollId, userId)
	if err != nil {
		return c.JSON(fiber.Map{
			"status":  true,
			"msg":     "success",
			"poll_id": pollId,
			"voted":   0,
		})
	}

	return c.JSON(fiber.Map{
		"status":  true,
		"msg":     "success",
		"poll_id": pollId,
		"voted":   voted,
	})
}

func HandleUserSubmitResponse(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	isAdmin := c.Locals("isAdmin")
	m := models.NewPollsModel()
	req := new(models.UserSubmitResponseReq)

	err := c.BodyParser(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	check := config.AppCnf.DoValidateReq(req)
	if len(check) > 0 {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    check,
		})
	}

	req.RoomId = roomId.(string)
	err = m.UserSubmitResponse(req, isAdmin.(bool))
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"status":  true,
		"msg":     "success",
		"poll_id": req.PollId,
	})
}

func HandleClosePoll(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	isAdmin := c.Locals("isAdmin")
	requestedUserId := c.Locals("requestedUserId")

	if !isAdmin.(bool) {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "only admin can perform this task",
		})
	}

	m := models.NewPollsModel()
	req := new(models.ClosePollReq)

	err := c.BodyParser(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	check := config.AppCnf.DoValidateReq(req)
	if len(check) > 0 {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    check,
		})
	}

	req.RoomId = roomId.(string)
	req.UserId = requestedUserId.(string)
	err = m.ClosePoll(req, isAdmin.(bool))
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"status":  true,
		"msg":     "success",
		"poll_id": req.PollId,
	})
}

func HandleGetPollResponsesDetails(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	pollId := c.Params("pollId")
	isAdmin := c.Locals("isAdmin")

	if !isAdmin.(bool) {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "only admin can perform this task",
		})
	}

	if pollId == "" {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "pollId required",
		})
	}

	m := models.NewPollsModel()
	err, responses := m.GetPollResponsesDetails(roomId.(string), pollId)

	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"status":    true,
		"msg":       "success",
		"poll_id":   pollId,
		"responses": responses,
	})
}

func HandleGetResponsesResult(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	pollId := c.Params("pollId")

	m := models.NewPollsModel()
	result, err := m.GetResponsesResult(roomId.(string), pollId)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"status":  true,
		"msg":     "success",
		"poll_id": pollId,
		"result":  result,
	})
}

func HandleGetPollsStats(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")

	m := models.NewPollsModel()
	stats, err := m.GetPollsStats(roomId.(string))
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"status": true,
		"msg":    "success",
		"stats":  stats,
	})
}
