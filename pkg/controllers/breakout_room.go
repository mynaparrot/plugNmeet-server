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
	res := new(plugnmeet.BreakoutRoomRes)
	res.Status = false

	if isAdmin != true {
		res.Msg = "only admin can perform this task"
		return SendBreakoutRoomResponse(c, res)
	}

	req := new(plugnmeet.CreateBreakoutRoomsReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		res.Msg = err.Error()
		return SendBreakoutRoomResponse(c, res)
	}

	req.RoomId = roomId.(string)
	req.RequestedUserId = requestedUserId.(string)

	m := models.NewBreakoutRoomModel()
	err = m.CreateBreakoutRooms(req)
	if err != nil {
		res.Msg = err.Error()
		return SendBreakoutRoomResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	return SendBreakoutRoomResponse(c, res)
}

func HandleJoinBreakoutRoom(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	isAdmin := c.Locals("isAdmin")

	res := new(plugnmeet.BreakoutRoomRes)
	res.Status = false

	req := new(plugnmeet.JoinBreakoutRoomReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		res.Msg = err.Error()
		return SendBreakoutRoomResponse(c, res)
	}

	req.RoomId = roomId.(string)
	req.IsAdmin = isAdmin.(bool)
	m := models.NewBreakoutRoomModel()
	token, err := m.JoinBreakoutRoom(req)
	if err != nil {
		res.Msg = err.Error()
		return SendBreakoutRoomResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.Token = &token
	return SendBreakoutRoomResponse(c, res)
}

func HandleGetBreakoutRooms(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	res := new(plugnmeet.BreakoutRoomRes)
	res.Status = false

	m := models.NewBreakoutRoomModel()
	rooms, err := m.GetBreakoutRooms(roomId.(string))
	if err != nil {
		res.Msg = err.Error()
		return SendBreakoutRoomResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.Rooms = rooms
	return SendBreakoutRoomResponse(c, res)
}

func HandleGetMyBreakoutRooms(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")
	res := new(plugnmeet.BreakoutRoomRes)
	res.Status = false

	m := models.NewBreakoutRoomModel()
	room, err := m.GetMyBreakoutRooms(roomId.(string), requestedUserId.(string))
	if err != nil {
		res.Msg = err.Error()
		return SendBreakoutRoomResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.Room = room
	return SendBreakoutRoomResponse(c, res)
}

func HandleIncreaseBreakoutRoomDuration(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	res := new(plugnmeet.BreakoutRoomRes)
	res.Status = false

	req := new(plugnmeet.IncreaseBreakoutRoomDurationReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		res.Msg = err.Error()
		return SendBreakoutRoomResponse(c, res)
	}

	req.RoomId = roomId.(string)
	m := models.NewBreakoutRoomModel()
	err = m.IncreaseBreakoutRoomDuration(req)
	if err != nil {
		res.Msg = err.Error()
		return SendBreakoutRoomResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	return SendBreakoutRoomResponse(c, res)
}

func HandleSendBreakoutRoomMsg(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	res := new(plugnmeet.BreakoutRoomRes)
	res.Status = false

	req := new(plugnmeet.BroadcastBreakoutRoomMsgReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		res.Msg = err.Error()
		return SendBreakoutRoomResponse(c, res)
	}

	req.RoomId = roomId.(string)
	m := models.NewBreakoutRoomModel()
	err = m.SendBreakoutRoomMsg(req)
	if err != nil {
		res.Msg = err.Error()
		return SendBreakoutRoomResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	return SendBreakoutRoomResponse(c, res)
}

func HandleEndBreakoutRoom(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	res := new(plugnmeet.BreakoutRoomRes)
	res.Status = false

	req := new(plugnmeet.EndBreakoutRoomReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		res.Msg = err.Error()
		return SendBreakoutRoomResponse(c, res)
	}

	req.RoomId = roomId.(string)
	m := models.NewBreakoutRoomModel()
	err = m.EndBreakoutRoom(req)
	if err != nil {
		res.Msg = err.Error()
		return SendBreakoutRoomResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	return SendBreakoutRoomResponse(c, res)
}

func HandleEndBreakoutRooms(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	isAdmin := c.Locals("isAdmin")
	res := new(plugnmeet.BreakoutRoomRes)
	res.Status = false

	if isAdmin != true {
		res.Msg = "only admin can perform this task"
		return SendBreakoutRoomResponse(c, res)
	}

	m := models.NewBreakoutRoomModel()
	err := m.EndBreakoutRooms(roomId.(string))
	if err != nil {
		res.Msg = err.Error()
		return SendBreakoutRoomResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	return SendBreakoutRoomResponse(c, res)
}

func SendBreakoutRoomResponse(c *fiber.Ctx, res *plugnmeet.BreakoutRoomRes) error {
	marshal, err := proto.Marshal(res)
	if err != nil {
		return err
	}
	c.Set("Content-Type", "application/protobuf")
	return c.Send(marshal)
}
