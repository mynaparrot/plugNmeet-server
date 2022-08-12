package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"google.golang.org/protobuf/proto"
)

func HandleUpdateUserLockSetting(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	isAdmin := c.Locals("isAdmin")
	requestedUserId := c.Locals("requestedUserId")

	if !isAdmin.(bool) {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "only admin can perform this task",
		})
	}

	m := models.NewUserModel()
	err := m.CommonValidation(c)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	req := new(models.UpdateUserLockSettingsReq)

	err = c.BodyParser(req)
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

	if roomId != req.RoomId {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "requested roomId & token roomId mismatched",
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

	req.RequestedUserId = requestedUserId.(string)
	err = m.UpdateUserLockSettings(req)
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

func HandleMuteUnMuteTrack(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	isAdmin := c.Locals("isAdmin")
	requestedUserId := c.Locals("requestedUserId")

	if !isAdmin.(bool) {
		return utils.SendCommonResponse(c, false, "only admin can perform this task")
	}

	m := models.NewUserModel()
	err := m.CommonValidation(c)
	if err != nil {
		return utils.SendCommonResponse(c, false, err.Error())
	}

	req := new(plugnmeet.MuteUnMuteTrackReq)
	err = proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonResponse(c, false, err.Error())
	}

	if roomId != req.RoomId {
		return utils.SendCommonResponse(c, false, "requested roomId & token roomId mismatched")
	}

	// now need to check if meeting is running or not
	rm := models.NewRoomModel()
	room, _ := rm.GetRoomInfo(req.RoomId, req.Sid, 1)

	if room.Id == 0 {
		return utils.SendCommonResponse(c, false, "room isn't running")
	}

	req.RequestedUserId = requestedUserId.(string)
	err = m.MuteUnMuteTrack(req)
	if err != nil {
		return utils.SendCommonResponse(c, false, err.Error())
	}

	return utils.SendCommonResponse(c, true, "success")
}

func HandleRemoveParticipant(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")
	isAdmin := c.Locals("isAdmin")

	if !isAdmin.(bool) {
		return utils.SendCommonResponse(c, false, "only admin can perform this task")
	}

	m := models.NewUserModel()
	err := m.CommonValidation(c)
	if err != nil {
		return utils.SendCommonResponse(c, false, err.Error())
	}

	req := new(plugnmeet.RemoveParticipantReq)
	err = proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonResponse(c, false, err.Error())
	}

	if roomId != req.RoomId {
		return utils.SendCommonResponse(c, false, "requested roomId & token roomId mismatched")
	}
	if requestedUserId == req.UserId {
		return utils.SendCommonResponse(c, false, "you can't remove yourself\"")
	}

	// now need to check if meeting is running or not
	rm := models.NewRoomModel()
	room, _ := rm.GetRoomInfo(req.RoomId, req.Sid, 1)

	if room.Id == 0 {
		return utils.SendCommonResponse(c, false, "room isn't running")
	}

	err = m.RemoveParticipant(req)
	if err != nil {
		return utils.SendCommonResponse(c, false, err.Error())
	}

	return utils.SendCommonResponse(c, true, "success")
}

func HandleSwitchPresenter(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")

	if !isAdmin.(bool) {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "only admin can perform this task",
		})
	}

	req := new(models.SwitchPresenterReq)
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

	m := models.NewUserModel()
	req.RoomId = roomId.(string)
	req.RequestedUserId = requestedUserId.(string)
	err = m.SwitchPresenter(req)

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
