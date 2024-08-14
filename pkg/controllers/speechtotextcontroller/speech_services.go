package speechtotextcontroller

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/speechtotextmodel"
	"google.golang.org/protobuf/proto"
)

func HandleSpeechToTextTranslationServiceStatus(c *fiber.Ctx) error {
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
	m := speechtotextmodel.New(nil, nil, nil, nil)
	err = m.SpeechToTextTranslationServiceStart(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

func HandleGenerateAzureToken(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")

	req := new(plugnmeet.GenerateAzureTokenReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}
	req.RoomId = roomId.(string)

	m := speechtotextmodel.New(nil, nil, nil, nil)
	err = m.GenerateAzureToken(req, requestedUserId.(string))
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

func HandleSpeechServiceUserStatus(c *fiber.Ctx) error {
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

	m := speechtotextmodel.New(nil, nil, nil, nil)
	err = m.SpeechServiceUserStatus(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

func HandleRenewAzureToken(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")

	req := new(plugnmeet.AzureTokenRenewReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}
	req.RoomId = roomId.(string)

	m := speechtotextmodel.New(nil, nil, nil, nil)
	err = m.RenewAzureToken(req, requestedUserId.(string))
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}
