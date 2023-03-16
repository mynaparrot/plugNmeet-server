package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"google.golang.org/protobuf/proto"
)

func HandleUpdateUserLockSetting(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	isAdmin := c.Locals("isAdmin")
	requestedUserId := c.Locals("requestedUserId")

	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	req := new(plugnmeet.UpdateUserLockSettingsReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	if roomId != req.RoomId {
		return utils.SendCommonProtobufResponse(c, false, "requested roomId & token roomId mismatched")
	}

	// now need to check if meeting is running or not
	rm := models.NewRoomModel()
	room, _ := rm.GetRoomInfo(req.RoomId, req.RoomSid, 1)

	if room.Id == 0 {
		return utils.SendCommonProtobufResponse(c, false, "room isn't running")
	}

	req.RequestedUserId = requestedUserId.(string)
	m := models.NewUserModel()
	err = m.UpdateUserLockSettings(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

func HandleMuteUnMuteTrack(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	isAdmin := c.Locals("isAdmin")
	requestedUserId := c.Locals("requestedUserId")

	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	m := models.NewUserModel()
	err := m.CommonValidation(c)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	req := new(plugnmeet.MuteUnMuteTrackReq)
	err = proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	if roomId != req.RoomId {
		return utils.SendCommonProtobufResponse(c, false, "requested roomId & token roomId mismatched")
	}

	// now need to check if meeting is running or not
	rm := models.NewRoomModel()
	room, _ := rm.GetRoomInfo(req.RoomId, req.Sid, 1)

	if room.Id == 0 {
		return utils.SendCommonProtobufResponse(c, false, "room isn't running")
	}

	req.RequestedUserId = requestedUserId.(string)
	err = m.MuteUnMuteTrack(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

func HandleRemoveParticipant(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")
	isAdmin := c.Locals("isAdmin")

	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	m := models.NewUserModel()
	err := m.CommonValidation(c)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	req := new(plugnmeet.RemoveParticipantReq)
	err = proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	if roomId != req.RoomId {
		return utils.SendCommonProtobufResponse(c, false, "requested roomId & token roomId mismatched")
	}
	if requestedUserId == req.UserId {
		return utils.SendCommonProtobufResponse(c, false, "you can't remove yourself\"")
	}

	// now need to check if meeting is running or not
	rm := models.NewRoomModel()
	room, _ := rm.GetRoomInfo(req.RoomId, req.Sid, 1)

	if room.Id == 0 {
		return utils.SendCommonProtobufResponse(c, false, "room isn't running")
	}

	err = m.RemoveParticipant(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

func HandleSwitchPresenter(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")

	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	req := new(plugnmeet.SwitchPresenterReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	m := models.NewUserModel()
	req.RoomId = roomId.(string)
	req.RequestedUserId = requestedUserId.(string)
	err = m.SwitchPresenter(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}
