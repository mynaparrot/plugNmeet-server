package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"google.golang.org/protobuf/proto"
)

func HandleSpeechToTextTranslationReq(c *fiber.Ctx) error {
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
	m := models.NewSpeechServices()
	err = m.SpeechToTextTranslationReq(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

func HandleGenerateAzureToken(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")
	req := new(plugnmeet.GenerateAzureTokenReq)
	req.RoomId = roomId.(string)

	m := models.NewSpeechServices()
	res, err := m.GenerateAzureToken(req, requestedUserId.(string))
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendProtobufResponse(c, res)
}

func HandleSpeechServiceUserStatus(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	req := new(plugnmeet.SpeechServiceUserStatusReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}
	if req.KeyId == "" {
		return utils.SendCommonProtobufResponse(c, false, "key_id required")
	}

	req.RoomId = roomId.(string)
	m := models.NewSpeechServices()
	err = m.SpeechServiceUserStatus(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}
