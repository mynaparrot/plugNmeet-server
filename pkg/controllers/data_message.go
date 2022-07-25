package controllers

import (
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugNmeet/pkg/config"
	"github.com/mynaparrot/plugNmeet/pkg/models"
)

func HandleDataMessage(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")
	isAdmin := c.Locals("isAdmin")

	if roomId == "" {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "no roomId in token",
		})
	}

	req := new(models.DataMessageReq)
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

	// now need to check if meeting is running or not
	rm := models.NewRoomModel()
	room, _ := rm.GetRoomInfo(req.RoomId, req.Sid, 1)

	if room.Id == 0 {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "room isn't running",
		})
	}

	if room.RoomId != roomId {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "roomId in token mismatched",
		})
	}

	req.RequestedUserId = fmt.Sprintf("%s", requestedUserId)
	if isAdmin != "" {
		req.IsAdmin = isAdmin.(bool)
	}

	err = models.NewDataMessage(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": true,
			"msg":    err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"status": true,
		"msg":    "success",
	})
}
