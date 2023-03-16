package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"google.golang.org/protobuf/proto"
)

func HandleExternalMediaPlayer(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")

	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	rid := roomId.(string)
	if rid == "" {
		return utils.SendCommonProtobufResponse(c, true, "roomId required")
	}

	req := new(plugnmeet.ExternalMediaPlayerReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, true, err.Error())
	}

	m := models.NewExternalMediaPlayerModel()
	req.RoomId = rid
	req.UserId = requestedUserId.(string)
	err = m.PerformTask(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, true, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}
