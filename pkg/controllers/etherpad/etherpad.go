package etherpadcontroller

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/etherpad"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"google.golang.org/protobuf/proto"
)

func HandleCreateEtherpad(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")

	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	if !config.GetConfig().SharedNotePad.Enabled {
		return utils.SendCommonProtobufResponse(c, false, "feature disabled")
	}

	rid := roomId.(string)
	if rid == "" {
		return utils.SendCommonProtobufResponse(c, false, "roomId required")
	}

	// now need to check if meeting is running or not
	rm := dbservice.New(config.GetConfig().DB)
	room, _ := rm.GetRoomInfoByRoomId(rid, 1)
	if room == nil || room.ID == 0 {
		return utils.SendCommonProtobufResponse(c, false, "room isn't active")
	}

	m := etherpadmodel.New(nil, nil, nil)
	res, err := m.CreateSession(rid, requestedUserId.(string))
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendProtobufResponse(c, res)
}

func HandleCleanPad(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	req := new(plugnmeet.CleanEtherpadReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	m := etherpadmodel.New(nil, nil, nil)
	err = m.CleanPad(req.RoomId, req.NodeId, req.PadId)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

func HandleChangeEtherpadStatus(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	req := new(plugnmeet.ChangeEtherpadStatusReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	m := etherpadmodel.New(nil, nil, nil)
	err = m.ChangeEtherpadStatus(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}
