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
