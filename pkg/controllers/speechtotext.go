package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"google.golang.org/protobuf/proto"
)

// SpeechToTextController holds dependencies for speech-to-text related handlers.
type SpeechToTextController struct {
	SpeechToTextModel *models.SpeechToTextModel
}

// NewSpeechToTextController creates a new SpeechToTextController.
func NewSpeechToTextController(m *models.SpeechToTextModel) *SpeechToTextController {
	return &SpeechToTextController{
		SpeechToTextModel: m,
	}
}

// HandleSpeechToTextTranslationServiceStatus handles enabling/disabling speech-to-text services.
func (stc *SpeechToTextController) HandleSpeechToTextTranslationServiceStatus(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")
	if isAdmin != true {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	req := new(plugnmeet.SpeechToTextTranslationReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	req.RoomId = roomId.(string)
	err = stc.SpeechToTextModel.SpeechToTextTranslationServiceStart(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

// HandleGenerateAzureToken handles generating an Azure token for speech services.
func (stc *SpeechToTextController) HandleGenerateAzureToken(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")

	req := new(plugnmeet.GenerateAzureTokenReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}
	req.RoomId = roomId.(string)

	err = stc.SpeechToTextModel.GenerateAzureToken(req, requestedUserId.(string))
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

// HandleSpeechServiceUserStatus handles updating a user's speech service status.
func (stc *SpeechToTextController) HandleSpeechServiceUserStatus(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")

	req := new(plugnmeet.SpeechServiceUserStatusReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}
	if req.KeyId == "" {
		return utils.SendCommonProtobufResponse(c, false, "key_id required")
	}

	req.RoomId = roomId.(string)
	req.UserId = requestedUserId.(string)

	err = stc.SpeechToTextModel.SpeechServiceUserStatus(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

// HandleRenewAzureToken handles renewing an Azure token.
func (stc *SpeechToTextController) HandleRenewAzureToken(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")

	req := new(plugnmeet.AzureTokenRenewReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}
	req.RoomId = roomId.(string)

	err = stc.SpeechToTextModel.RenewAzureToken(req, requestedUserId.(string))
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}
