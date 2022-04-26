package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugNmeet/internal/config"
	"github.com/mynaparrot/plugNmeet/internal/models"
)

func HandleExternalMediaPlayer(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")

	if !isAdmin.(bool) {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "only admin can perform this task",
		})
	}

	rid := roomId.(string)
	if rid == "" {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "roomId required",
		})
	}

	req := new(models.ExternalMediaPlayerReq)
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

	m := models.NewExternalMediaPlayerModel()
	req.RoomId = rid
	req.UserId = requestedUserId.(string)
	err = m.PerformTask(req)

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
