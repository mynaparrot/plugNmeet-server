package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugNmeet/internal/config"
	"github.com/mynaparrot/plugNmeet/internal/models"
)

func HandleCreateBreakoutRooms(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")

	if isAdmin != true {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "only admin can perform this task",
		})
	}

	req := new(models.CreateBreakoutRoomsReq)
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
	req.RequestedUserId = requestedUserId.(string)

	m := models.NewBreakoutRoomModel()
	err = m.CreateBreakoutRooms(req)
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

func HandleJoinBreakoutRoom(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")

	req := new(models.JoinBreakoutRoomReq)
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
	m := models.NewBreakoutRoomModel()
	token, err := m.JoinBreakoutRoom(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": true,
			"msg":    "success",
		})
	}

	return c.JSON(fiber.Map{
		"status": true,
		"msg":    "success",
		"token":  token,
	})
}
