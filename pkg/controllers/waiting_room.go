package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"google.golang.org/protobuf/proto"
)

// WaitingRoomController holds dependencies for waiting room-related handlers.
type WaitingRoomController struct {
	WaitingRoomModel *models.WaitingRoomModel
}

// NewWaitingRoomController creates a new WaitingRoomController.
func NewWaitingRoomController(m *models.WaitingRoomModel) *WaitingRoomController {
	return &WaitingRoomController{
		WaitingRoomModel: m,
	}
}

// HandleApproveUsers handles approving users from the waiting room.
func (wrc *WaitingRoomController) HandleApproveUsers(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	isAdmin := c.Locals("isAdmin")

	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	req := new(plugnmeet.ApproveWaitingUsersReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	req.RoomId = roomId.(string)
	err = wrc.WaitingRoomModel.ApproveWaitingUsers(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

// HandleUpdateWaitingRoomMessage handles updating the waiting room message.
func (wrc *WaitingRoomController) HandleUpdateWaitingRoomMessage(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	isAdmin := c.Locals("isAdmin")

	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	req := new(plugnmeet.UpdateWaitingRoomMessageReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	req.RoomId = roomId.(string)
	err = wrc.WaitingRoomModel.UpdateWaitingRoomMessage(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}
