package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugNmeet/internal/config"
	"github.com/mynaparrot/plugNmeet/internal/models"
)

func HandleCreatePoll(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	isAdmin := c.Locals("isAdmin")

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
	err = m.CreatePoll(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"status": true,
		"msg":    "success",
	})
}

func HandleListPolls(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	userId := c.Params("userId")

	if userId == "" {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "userId required",
		})
	}

	m := models.NewPollsModel()
	err, polls := m.ListPolls(roomId.(string), userId)
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

func HandlePollResponses(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	pollId := c.Params("pollId")

	if pollId == "" {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "pollId required",
		})
	}

	m := models.NewPollsModel()
	err, responses := m.GetPollResponses(roomId.(string), pollId)

	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"status":    true,
		"msg":       "success",
		"responses": responses,
	})
}

func HandleUserSubmitResponse(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
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
	err = m.UserSubmitResponse(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"status": true,
		"msg":    "success",
	})
}
