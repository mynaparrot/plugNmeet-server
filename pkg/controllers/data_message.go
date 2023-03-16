package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"google.golang.org/protobuf/proto"
)

func HandleDataMessage(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")
	isAdmin := c.Locals("isAdmin")

	if roomId == "" {
		return utils.SendCommonProtobufResponse(c, false, "no roomId in token")
	}

	req := new(plugnmeet.DataMessageReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	// now need to check if meeting is running or not
	rm := models.NewRoomModel()
	room, _ := rm.GetRoomInfo(req.RoomId, req.RoomSid, 1)

	if room.Id == 0 {
		return utils.SendCommonProtobufResponse(c, false, "room isn't running")
	}

	if room.RoomId != roomId {
		return utils.SendCommonProtobufResponse(c, false, "roomId in token mismatched")
	}

	req.RequestedUserId = requestedUserId.(string)
	req.IsAdmin = isAdmin.(bool)
	m := models.NewDataMessageModel()
	err = m.SendDataMessage(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}
