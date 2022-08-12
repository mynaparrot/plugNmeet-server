package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"google.golang.org/protobuf/proto"
)

func HandleExternalDisplayLink(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")

	if !isAdmin.(bool) {
		return utils.SendCommonResponse(c, false, "only admin can perform this task")
	}

	rid := roomId.(string)
	if rid == "" {
		return utils.SendCommonResponse(c, false, "roomId required")
	}

	req := new(plugnmeet.ExternalDisplayLinkReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonResponse(c, false, err.Error())
	}

	m := models.NewExternalDisplayLinkModel()
	req.RoomId = rid
	req.UserId = requestedUserId.(string)
	err = m.PerformTask(req)

	if err != nil {
		return utils.SendCommonResponse(c, false, err.Error())
	}

	return utils.SendCommonResponse(c, true, "success")
}
