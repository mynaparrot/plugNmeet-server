package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func HandleRoomCreate(c *fiber.Ctx) error {
	op := protojson.UnmarshalOptions{
		DiscardUnknown: true,
	}
	req := new(plugnmeet.CreateRoomReq)
	err := op.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	if err = req.Validate(); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	if err = req.Metadata.Validate(); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	if err = req.Metadata.RoomFeatures.Validate(); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	m := models.NewRoomAuthModel()
	status, msg, room := m.CreateRoom(req)

	r := &plugnmeet.CreateRoomRes{
		Status:   status,
		Msg:      msg,
		RoomInfo: room,
	}

	return utils.SendProtoJsonResponse(c, r)
}

func HandleIsRoomActive(c *fiber.Ctx) error {
	req := new(plugnmeet.IsRoomActiveReq)
	op := protojson.UnmarshalOptions{
		DiscardUnknown: true,
	}
	err := op.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}
	if req.RoomId == "" {
		return utils.SendCommonProtoJsonResponse(c, false, "room_id required")
	}

	m := models.NewRoomAuthModel()
	status, msg := m.IsRoomActive(req)
	return utils.SendCommonProtoJsonResponse(c, status, msg)
}

func HandleGetActiveRoomInfo(c *fiber.Ctx) error {
	req := new(plugnmeet.GetActiveRoomInfoReq)
	op := protojson.UnmarshalOptions{
		DiscardUnknown: true,
	}
	err := op.Unmarshal(c.Body(), req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}
	if req.RoomId == "" {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "room_id required",
		})
	}
	m := models.NewRoomAuthModel()
	status, msg, res := m.GetActiveRoomInfo(req)

	r := &plugnmeet.GetActiveRoomInfoRes{
		Status: status,
		Msg:    msg,
		Room:   res,
	}

	return utils.SendProtoJsonResponse(c, r)
}

func HandleGetActiveRoomsInfo(c *fiber.Ctx) error {
	m := models.NewRoomAuthModel()
	status, msg, res := m.GetActiveRoomsInfo()

	r := &plugnmeet.GetActiveRoomsInfoRes{
		Status: status,
		Msg:    msg,
		Rooms:  res,
	}

	return utils.SendProtoJsonResponse(c, r)
}

func HandleEndRoom(c *fiber.Ctx) error {
	req := new(plugnmeet.RoomEndReq)
	op := protojson.UnmarshalOptions{
		DiscardUnknown: true,
	}
	err := op.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}
	if req.RoomId == "" {
		return utils.SendCommonProtoJsonResponse(c, false, "room_id required")
	}

	m := models.NewRoomAuthModel()
	status, msg := m.EndRoom(req)

	return utils.SendCommonProtoJsonResponse(c, status, msg)
}

func HandleEndRoomForAPI(c *fiber.Ctx) error {
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

	m := models.NewRoomAuthModel()
	status, msg := m.EndRoom(req)
	return utils.SendCommonProtobufResponse(c, status, msg)
}

func HandleChangeVisibilityForAPI(c *fiber.Ctx) error {
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

	m := models.NewRoomAuthModel()
	status, msg := m.ChangeVisibility(req)
	return utils.SendCommonProtobufResponse(c, status, msg)
}
