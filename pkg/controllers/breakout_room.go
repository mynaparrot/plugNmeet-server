package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"google.golang.org/protobuf/proto"
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

	req := new(plugnmeet.CreateBreakoutRoomsReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	err = req.Validate()
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
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
	isAdmin := c.Locals("isAdmin")
	req := new(plugnmeet.JoinBreakoutRoomReq)

	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	req.RoomId = roomId.(string)
	req.IsAdmin = isAdmin.(bool)
	m := models.NewBreakoutRoomModel()
	token, err := m.JoinBreakoutRoom(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"status": true,
		"msg":    "success",
		"token":  token,
	})
}

func HandleGetBreakoutRooms(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	m := models.NewBreakoutRoomModel()
	rooms, err := m.GetBreakoutRooms(roomId.(string))
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"status": true,
		"msg":    "success",
		"rooms":  rooms,
	})
}

func HandleGetMyBreakoutRooms(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")

	m := models.NewBreakoutRoomModel()
	room, err := m.GetMyBreakoutRooms(roomId.(string), requestedUserId.(string))
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"status": true,
		"msg":    "success",
		"room":   room,
	})
}

func HandleIncreaseBreakoutRoomDuration(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	req := new(plugnmeet.IncreaseBreakoutRoomDurationReq)

	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	req.RoomId = roomId.(string)
	m := models.NewBreakoutRoomModel()
	err = m.IncreaseBreakoutRoomDuration(req)
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

func HandleSendBreakoutRoomMsg(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	req := new(plugnmeet.BroadcastBreakoutRoomMsgReq)

	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	req.RoomId = roomId.(string)
	m := models.NewBreakoutRoomModel()
	err = m.SendBreakoutRoomMsg(req)
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

func HandleEndBreakoutRoom(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	req := new(plugnmeet.EndBreakoutRoomReq)

	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	req.RoomId = roomId.(string)
	m := models.NewBreakoutRoomModel()
	err = m.EndBreakoutRoom(req)
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

func HandleEndBreakoutRooms(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	isAdmin := c.Locals("isAdmin")

	if isAdmin != true {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "only admin can perform this task",
		})
	}

	m := models.NewBreakoutRoomModel()
	err := m.EndBreakoutRooms(roomId.(string))

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
