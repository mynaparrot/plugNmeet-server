package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"google.golang.org/protobuf/proto"
)

func HandleCreateEtherpad(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")

	if !isAdmin.(bool) {
		return utils.SendCommonResponse(c, false, "only admin can perform this task")
	}

	if !config.AppCnf.SharedNotePad.Enabled {
		return utils.SendCommonResponse(c, false, "feature disabled")
	}

	rid := roomId.(string)
	if rid == "" {
		return utils.SendCommonResponse(c, false, "roomId required")
	}

	// now need to check if meeting is running or not
	rm := models.NewRoomModel()
	room, _ := rm.GetRoomInfo(rid, "", 1)
	if room.Id == 0 {
		return utils.SendCommonResponse(c, false, "room isn't active")
	}

	m := models.NewEtherpadModel()
	res, err := m.CreateSession(rid)
	if err != nil {
		return utils.SendCommonResponse(c, false, err.Error())
	}

	return utils.SendProtoResponse(c, res)
}

func HandleCleanPad(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	if !isAdmin.(bool) {
		return utils.SendCommonResponse(c, false, "only admin can perform this task")
	}

	req := new(plugnmeet.CleanEtherpadReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonResponse(c, false, err.Error())
	}

	m := models.NewEtherpadModel()
	err = m.CleanPad(req.RoomId, req.NodeId, req.PadId)
	if err != nil {
		return utils.SendCommonResponse(c, false, err.Error())
	}

	return utils.SendCommonResponse(c, true, "success")
}

func HandleChangeEtherpadStatus(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	if !isAdmin.(bool) {
		return utils.SendCommonResponse(c, false, "only admin can perform this task")
	}

	req := new(plugnmeet.ChangeEtherpadStatusReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonResponse(c, false, err.Error())
	}

	m := models.NewEtherpadModel()
	err = m.ChangeEtherpadStatus(req)
	if err != nil {
		return utils.SendCommonResponse(c, false, err.Error())
	}

	return utils.SendCommonResponse(c, true, "success")
}
