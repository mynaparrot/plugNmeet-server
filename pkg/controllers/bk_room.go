package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"google.golang.org/protobuf/proto"
)

// BreakoutRoomController holds dependencies for breakout room handlers.
type BreakoutRoomController struct {
	BreakoutRoomModel *models.BreakoutRoomModel
}

// NewBreakoutRoomController creates a new BreakoutRoomController.
func NewBreakoutRoomController(brm *models.BreakoutRoomModel) *BreakoutRoomController {
	return &BreakoutRoomController{
		BreakoutRoomModel: brm,
	}
}

// HandleCreateBreakoutRooms handles creating breakout rooms.
func (brc *BreakoutRoomController) HandleCreateBreakoutRooms(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")
	res := new(plugnmeet.BreakoutRoomRes)
	res.Status = false

	if isAdmin != true {
		res.Msg = "only admin can perform this task"
		return sendBreakoutRoomResponse(c, res)
	}

	req := new(plugnmeet.CreateBreakoutRoomsReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		res.Msg = err.Error()
		return sendBreakoutRoomResponse(c, res)
	}

	req.RoomId = roomId.(string)
	req.RequestedUserId = requestedUserId.(string)

	err = brc.BreakoutRoomModel.CreateBreakoutRooms(req)
	if err != nil {
		res.Msg = err.Error()
		return sendBreakoutRoomResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	return sendBreakoutRoomResponse(c, res)
}

// HandleJoinBreakoutRoom handles joining a breakout room.
func (brc *BreakoutRoomController) HandleJoinBreakoutRoom(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	isAdmin := c.Locals("isAdmin")

	res := new(plugnmeet.BreakoutRoomRes)
	res.Status = false

	req := new(plugnmeet.JoinBreakoutRoomReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		res.Msg = err.Error()
		return sendBreakoutRoomResponse(c, res)
	}

	req.RoomId = roomId.(string)
	req.IsAdmin = isAdmin.(bool)
	token, err := brc.BreakoutRoomModel.JoinBreakoutRoom(c.UserContext(), req)
	if err != nil {
		res.Msg = err.Error()
		return sendBreakoutRoomResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.Token = &token
	return sendBreakoutRoomResponse(c, res)
}

// HandleGetBreakoutRooms lists all breakout rooms for a parent room.
func (brc *BreakoutRoomController) HandleGetBreakoutRooms(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	res := new(plugnmeet.BreakoutRoomRes)
	res.Status = false

	rooms, err := brc.BreakoutRoomModel.GetBreakoutRooms(roomId.(string))
	if err != nil {
		res.Msg = err.Error()
		return sendBreakoutRoomResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.Rooms = rooms
	return sendBreakoutRoomResponse(c, res)
}

// HandleGetMyBreakoutRooms gets the breakout room a user belongs to.
func (brc *BreakoutRoomController) HandleGetMyBreakoutRooms(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")
	res := new(plugnmeet.BreakoutRoomRes)
	res.Status = false

	room, err := brc.BreakoutRoomModel.GetMyBreakoutRooms(roomId.(string), requestedUserId.(string))
	if err != nil {
		res.Msg = err.Error()
		return sendBreakoutRoomResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.Room = room
	return sendBreakoutRoomResponse(c, res)
}

// HandleIncreaseBreakoutRoomDuration increases the duration of a breakout room.
func (brc *BreakoutRoomController) HandleIncreaseBreakoutRoomDuration(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	res := new(plugnmeet.BreakoutRoomRes)
	res.Status = false

	req := new(plugnmeet.IncreaseBreakoutRoomDurationReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		res.Msg = err.Error()
		return sendBreakoutRoomResponse(c, res)
	}

	req.RoomId = roomId.(string)
	err = brc.BreakoutRoomModel.IncreaseBreakoutRoomDuration(req)
	if err != nil {
		res.Msg = err.Error()
		return sendBreakoutRoomResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	return sendBreakoutRoomResponse(c, res)
}

// HandleSendBreakoutRoomMsg broadcasts a message to all breakout rooms.
func (brc *BreakoutRoomController) HandleSendBreakoutRoomMsg(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	res := new(plugnmeet.BreakoutRoomRes)
	res.Status = false

	req := new(plugnmeet.BroadcastBreakoutRoomMsgReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		res.Msg = err.Error()
		return sendBreakoutRoomResponse(c, res)
	}

	req.RoomId = roomId.(string)
	err = brc.BreakoutRoomModel.SendBreakoutRoomMsg(req)
	if err != nil {
		res.Msg = err.Error()
		return sendBreakoutRoomResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	return sendBreakoutRoomResponse(c, res)
}

// HandleEndBreakoutRoom ends a specific breakout room.
func (brc *BreakoutRoomController) HandleEndBreakoutRoom(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	res := new(plugnmeet.BreakoutRoomRes)
	res.Status = false

	req := new(plugnmeet.EndBreakoutRoomReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		res.Msg = err.Error()
		return sendBreakoutRoomResponse(c, res)
	}

	req.RoomId = roomId.(string)
	err = brc.BreakoutRoomModel.EndBreakoutRoom(c.UserContext(), req)
	if err != nil {
		res.Msg = err.Error()
		return sendBreakoutRoomResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	return sendBreakoutRoomResponse(c, res)
}

// HandleEndBreakoutRooms ends all breakout rooms for a parent room.
func (brc *BreakoutRoomController) HandleEndBreakoutRooms(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	isAdmin := c.Locals("isAdmin")
	res := new(plugnmeet.BreakoutRoomRes)
	res.Status = false

	if isAdmin != true {
		res.Msg = "only admin can perform this task"
		return sendBreakoutRoomResponse(c, res)
	}

	err := brc.BreakoutRoomModel.EndAllBreakoutRoomsByParentRoomId(c.UserContext(), roomId.(string))
	if err != nil {
		res.Msg = err.Error()
		return sendBreakoutRoomResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	return sendBreakoutRoomResponse(c, res)
}

func sendBreakoutRoomResponse(c *fiber.Ctx, res *plugnmeet.BreakoutRoomRes) error {
	marshal, err := proto.Marshal(res)
	if err != nil {
		return err
	}
	c.Set("Content-Type", "application/protobuf")
	return c.Send(marshal)
}
