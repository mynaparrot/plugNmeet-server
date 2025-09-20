package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"google.golang.org/protobuf/proto"
)

// RoomController holds dependencies for room-related handlers.
type RoomController struct {
	RoomModel *models.RoomModel
}

// NewRoomController creates a new RoomController.
func NewRoomController(m *models.RoomModel) *RoomController {
	return &RoomController{
		RoomModel: m,
	}
}

// HandleRoomCreate handles creating a new room.
func (rc *RoomController) HandleRoomCreate(c *fiber.Ctx) error {
	req := new(plugnmeet.CreateRoomReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	room, err := rc.RoomModel.CreateRoom(req)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	r := &plugnmeet.CreateRoomRes{
		Status:   true,
		Msg:      "success",
		RoomInfo: room,
	}

	return utils.SendProtoJsonResponse(c, r)
}

// HandleIsRoomActive checks if a room is active.
func (rc *RoomController) HandleIsRoomActive(c *fiber.Ctx) error {
	req := new(plugnmeet.IsRoomActiveReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	res, _, _, _ := rc.RoomModel.IsRoomActive(c.UserContext(), req)
	return utils.SendProtoJsonResponse(c, res)
}

// HandleGetActiveRoomInfo gets information about an active room.
func (rc *RoomController) HandleGetActiveRoomInfo(c *fiber.Ctx) error {
	req := new(plugnmeet.GetActiveRoomInfoReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	status, msg, res := rc.RoomModel.GetActiveRoomInfo(c.UserContext(), req)

	r := &plugnmeet.GetActiveRoomInfoRes{
		Status: status,
		Msg:    msg,
		Room:   res,
	}

	return utils.SendProtoJsonResponse(c, r)
}

// HandleGetActiveRoomsInfo gets information about all active rooms.
func (rc *RoomController) HandleGetActiveRoomsInfo(c *fiber.Ctx) error {
	status, msg, res := rc.RoomModel.GetActiveRoomsInfo()

	r := &plugnmeet.GetActiveRoomsInfoRes{
		Status: status,
		Msg:    msg,
		Rooms:  res,
	}

	return utils.SendProtoJsonResponse(c, r)
}

// HandleEndRoom handles ending a room.
func (rc *RoomController) HandleEndRoom(c *fiber.Ctx) error {
	req := new(plugnmeet.RoomEndReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	status, msg := rc.RoomModel.EndRoom(c.UserContext(), req)

	return utils.SendCommonProtoJsonResponse(c, status, msg)
}

// HandleFetchPastRooms handles fetching past rooms.
func (rc *RoomController) HandleFetchPastRooms(c *fiber.Ctx) error {
	req := new(plugnmeet.FetchPastRoomsReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	result, err := rc.RoomModel.FetchPastRooms(req)

	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}
	if result.GetTotalRooms() == 0 {
		return utils.SendCommonProtoJsonResponse(c, false, "no info found")
	}

	r := &plugnmeet.FetchPastRoomsRes{
		Status: true,
		Msg:    "success",
		Result: result,
	}
	return utils.SendProtoJsonResponse(c, r)
}

// HandleEndRoomForAPI handles ending a room via API call.
func (rc *RoomController) HandleEndRoomForAPI(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")

	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	req := new(plugnmeet.RoomEndReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	if roomId != req.RoomId {
		return utils.SendCommonProtobufResponse(c, false, "requested roomId & token roomId mismatched")
	}

	status, msg := rc.RoomModel.EndRoom(c.UserContext(), req)
	return utils.SendCommonProtobufResponse(c, status, msg)
}

// HandleChangeVisibilityForAPI handles changing room visibility via API call.
func (rc *RoomController) HandleChangeVisibilityForAPI(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")

	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	req := new(plugnmeet.ChangeVisibilityRes)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	if roomId != req.RoomId {
		return utils.SendCommonProtobufResponse(c, false, "requested roomId & token roomId mismatched")
	}

	status, msg := rc.RoomModel.ChangeVisibility(req)
	return utils.SendCommonProtobufResponse(c, status, msg)
}
