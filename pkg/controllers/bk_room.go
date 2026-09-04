package controllers

import (
	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"go.uber.org/fx"
	"google.golang.org/protobuf/proto"
)

// BreakoutRoomController holds dependencies for breakout room handlers.
type BreakoutRoomController struct {
	BreakoutRoomModel *models.BreakoutRoomModel
}

type BreakoutRoomControllerArgs struct {
	fx.In
	BreakoutRoomModel *models.BreakoutRoomModel
}

// NewBreakoutRoomController creates a new BreakoutRoomController.
func NewBreakoutRoomController(args BreakoutRoomControllerArgs) *BreakoutRoomController {
	return &BreakoutRoomController{
		BreakoutRoomModel: args.BreakoutRoomModel,
	}
}

// HandleCreateBreakoutRooms handles creating breakout rooms.
func (brc *BreakoutRoomController) HandleCreateBreakoutRooms(c fiber.Ctx) error {
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	roomId := fiber.Locals[string](c, "roomId")
	requestedUserId := fiber.Locals[string](c, "requestedUserId")
	res := new(plugnmeet.BreakoutRoomRes)
	res.Status = false

	if isAdmin != true {
		res.Msg = "only admin can perform this task"
		return utils.SendProtobufResponse(c, res)
	}

	req := new(plugnmeet.CreateBreakoutRoomsReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	req.RoomId = roomId
	req.RequestedUserId = requestedUserId

	rooms, err := brc.BreakoutRoomModel.CreateBreakoutRooms(c.RequestCtx(), req)
	if err != nil {
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	// Return the created rooms (with roomSids) so the creating client can derive
	// the child-room encryption keys needed for the later whiteboard seeding flow.
	res.Rooms = rooms
	return utils.SendProtobufResponse(c, res)
}

// HandleJoinBreakoutRoom handles joining a breakout room.
func (brc *BreakoutRoomController) HandleJoinBreakoutRoom(c fiber.Ctx) error {
	roomId := brc.BreakoutRoomModel.ResolveParentRoomId(fiber.Locals[string](c, "roomId"))
	isAdmin := fiber.Locals[bool](c, "isAdmin")

	res := new(plugnmeet.BreakoutRoomRes)
	res.Status = false

	req := new(plugnmeet.JoinBreakoutRoomReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	req.RoomId = roomId
	token, err := brc.BreakoutRoomModel.JoinBreakoutRoom(c.RequestCtx(), req, isAdmin)
	if err != nil {
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.Token = &token
	return utils.SendProtobufResponse(c, res)
}

// HandleGetBreakoutRooms lists all breakout rooms for a parent room.
func (brc *BreakoutRoomController) HandleGetBreakoutRooms(c fiber.Ctx) error {
	roomId := brc.BreakoutRoomModel.ResolveParentRoomId(fiber.Locals[string](c, "roomId"))
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	userId := fiber.Locals[string](c, "requestedUserId")
	res := new(plugnmeet.BreakoutRoomRes)
	res.Status = false

	rooms, err := brc.BreakoutRoomModel.GetBreakoutRooms(roomId, userId, isAdmin)
	if err != nil {
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.Rooms = rooms
	return utils.SendProtobufResponse(c, res)
}

// HandleGetMyBreakoutRooms gets the breakout room a user belongs to.
func (brc *BreakoutRoomController) HandleGetMyBreakoutRooms(c fiber.Ctx) error {
	roomId := brc.BreakoutRoomModel.ResolveParentRoomId(fiber.Locals[string](c, "roomId"))
	requestedUserId := fiber.Locals[string](c, "requestedUserId")
	res := new(plugnmeet.BreakoutRoomRes)
	res.Status = false

	room, err := brc.BreakoutRoomModel.GetMyBreakoutRooms(roomId, requestedUserId)
	if err != nil {
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.Room = room
	return utils.SendProtobufResponse(c, res)
}

// HandleIncreaseBreakoutRoomDuration increases the duration of a breakout room.
func (brc *BreakoutRoomController) HandleIncreaseBreakoutRoomDuration(c fiber.Ctx) error {
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	roomId := brc.BreakoutRoomModel.ResolveParentRoomId(fiber.Locals[string](c, "roomId"))
	res := new(plugnmeet.BreakoutRoomRes)
	res.Status = false

	req := new(plugnmeet.IncreaseBreakoutRoomDurationReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	if isAdmin != true {
		res.Msg = "only admin can perform this task"
		return utils.SendProtobufResponse(c, res)
	}

	req.RoomId = roomId
	err = brc.BreakoutRoomModel.IncreaseBreakoutRoomDuration(req)
	if err != nil {
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	return utils.SendProtobufResponse(c, res)
}

// HandleSendBreakoutRoomMsg broadcasts a message to all breakout rooms.
func (brc *BreakoutRoomController) HandleSendBreakoutRoomMsg(c fiber.Ctx) error {
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	roomId := brc.BreakoutRoomModel.ResolveParentRoomId(fiber.Locals[string](c, "roomId"))
	res := new(plugnmeet.BreakoutRoomRes)
	res.Status = false

	req := new(plugnmeet.BroadcastBreakoutRoomMsgReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	if isAdmin != true {
		res.Msg = "only admin can perform this task"
		return utils.SendProtobufResponse(c, res)
	}

	req.RoomId = roomId
	if err := brc.BreakoutRoomModel.SendBreakoutRoomMsg(req); err != nil {
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	return utils.SendProtobufResponse(c, res)
}

// HandleEndBreakoutRoom ends a specific breakout room.
func (brc *BreakoutRoomController) HandleEndBreakoutRoom(c fiber.Ctx) error {
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	roomId := brc.BreakoutRoomModel.ResolveParentRoomId(fiber.Locals[string](c, "roomId"))
	res := new(plugnmeet.BreakoutRoomRes)
	res.Status = false

	req := new(plugnmeet.EndBreakoutRoomReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	if isAdmin != true {
		res.Msg = "only admin can perform this task"
		return utils.SendProtobufResponse(c, res)
	}

	req.RoomId = roomId
	if err := brc.BreakoutRoomModel.EndBreakoutRoom(c.RequestCtx(), req); err != nil {
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	return utils.SendProtobufResponse(c, res)
}

// HandleBackToMainRoom handles returning a user from a breakout room to the main room.
func (brc *BreakoutRoomController) HandleBackToMainRoom(c fiber.Ctx) error {
	// derive identity from the auth context (breakout-room token scope), not the body
	roomId := fiber.Locals[string](c, "roomId")
	userId := fiber.Locals[string](c, "requestedUserId")

	res := new(plugnmeet.BackToMainRoomRes)
	res.Status = false

	req := new(plugnmeet.BackToMainRoomReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	// always trust the token scope over any client-supplied values
	req.RoomId = roomId
	req.UserId = userId

	token, err := brc.BreakoutRoomModel.BackToMainRoom(c.RequestCtx(), req)
	if err != nil {
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.Token = &token
	return utils.SendProtobufResponse(c, res)
}

// HandleEndBreakoutRooms ends all breakout rooms for a parent room.
func (brc *BreakoutRoomController) HandleEndBreakoutRooms(c fiber.Ctx) error {
	roomId := brc.BreakoutRoomModel.ResolveParentRoomId(fiber.Locals[string](c, "roomId"))
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	res := new(plugnmeet.BreakoutRoomRes)
	res.Status = false

	if isAdmin != true {
		res.Msg = "only admin can perform this task"
		return utils.SendProtobufResponse(c, res)
	}

	if err := brc.BreakoutRoomModel.EndAllBreakoutRoomsByParentRoomId(c.RequestCtx(), roomId); err != nil {
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	return utils.SendProtobufResponse(c, res)
}

// HandleReInviteBreakoutRoom re-sends a breakout room invitation (JOIN_BREAKOUT_ROOM)
// to a user who is assigned to the room but did not join.
func (brc *BreakoutRoomController) HandleReInviteBreakoutRoom(c fiber.Ctx) error {
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	roomId := brc.BreakoutRoomModel.ResolveParentRoomId(fiber.Locals[string](c, "roomId"))

	res := new(plugnmeet.BreakoutRoomRes)
	res.Status = false

	if isAdmin != true {
		res.Msg = "only admin can perform this task"
		return utils.SendProtobufResponse(c, res)
	}

	req := new(plugnmeet.ReInviteBreakoutRoomReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	req.RoomId = roomId
	if err := brc.BreakoutRoomModel.ReInviteBreakoutRoom(c.RequestCtx(), req); err != nil {
		res.Msg = err.Error()
		return utils.SendProtobufResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	return utils.SendProtobufResponse(c, res)
}
