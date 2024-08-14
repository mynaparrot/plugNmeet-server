package datamsgcontroller

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/datamsgmodel"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/dbservice"
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
	app := config.GetConfig()
	ds := dbservice.New(app.ORM)
	isRunning := 1
	room, _ := ds.GetRoomInfoBySid(req.RoomSid, &isRunning)

	if room == nil || room.ID == 0 {
		return utils.SendCommonProtobufResponse(c, false, "room isn't running")
	}

	if room.RoomId != roomId {
		return utils.SendCommonProtobufResponse(c, false, "roomId in token mismatched")
	}

	req.RequestedUserId = requestedUserId.(string)
	req.IsAdmin = isAdmin.(bool)
	m := datamsgmodel.New(app, ds, nil, nil)
	err = m.SendDataMessage(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}
