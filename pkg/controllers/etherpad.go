package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"google.golang.org/protobuf/proto"
)

// EtherpadController holds dependencies for etherpad-related handlers.
type EtherpadController struct {
	AppConfig     *config.AppConfig
	EtherpadModel *models.EtherpadModel
	RoomModel     *models.RoomModel
}

// NewEtherpadController creates a new EtherpadController.
func NewEtherpadController(config *config.AppConfig, em *models.EtherpadModel, rm *models.RoomModel) *EtherpadController {
	return &EtherpadController{
		AppConfig:     config,
		EtherpadModel: em,
		RoomModel:     rm,
	}
}

// HandleCreateEtherpad handles the creation of an etherpad session.
func (ec *EtherpadController) HandleCreateEtherpad(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")

	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	if !ec.AppConfig.SharedNotePad.Enabled {
		return utils.SendCommonProtobufResponse(c, false, "feature disabled")
	}

	rid := roomId.(string)
	if rid == "" {
		return utils.SendCommonProtobufResponse(c, false, "roomId required")
	}

	// check if meeting is running
	res, _, _, _ := ec.RoomModel.IsRoomActive(c.UserContext(), &plugnmeet.IsRoomActiveReq{
		RoomId: rid,
	})
	if !res.GetIsActive() {
		return utils.SendCommonProtobufResponse(c, false, "room is not active")
	}

	result, err := ec.EtherpadModel.CreateSession(rid, requestedUserId.(string))
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendProtobufResponse(c, result)
}

// HandleCleanPad handles cleaning an etherpad pad.
func (ec *EtherpadController) HandleCleanPad(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	req := new(plugnmeet.CleanEtherpadReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	err = ec.EtherpadModel.CleanPad(req.RoomId, req.NodeId, req.PadId)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

// HandleChangeEtherpadStatus handles changing the public status of an etherpad.
func (ec *EtherpadController) HandleChangeEtherpadStatus(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	req := new(plugnmeet.ChangeEtherpadStatusReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	err = ec.EtherpadModel.ChangeEtherpadStatus(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}
