package controllers

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/version"
	"google.golang.org/protobuf/proto"
	"strings"
)

// HandleAuthHeaderCheck will check auth values
// It will accept 2 header values: API-KEY & HASH-SIGNATURE
// HASH-SIGNATURE will require to calculated hmac sha256 using
// body + Secret key
func HandleAuthHeaderCheck(c *fiber.Ctx) error {
	apiKey := c.Get("API-KEY", "")
	signature := c.Get("HASH-SIGNATURE", "")
	body := c.Body()

	if apiKey != config.GetConfig().Client.ApiKey {
		c.Status(fiber.StatusUnauthorized)
		return utils.SendCommonProtoJsonResponse(c, false, "invalid API key")
	}
	if signature == "" {
		c.Status(fiber.StatusUnauthorized)
		return utils.SendCommonProtoJsonResponse(c, false, "hash signature value required")
	}

	status := false
	mac := hmac.New(sha256.New, []byte(config.GetConfig().Client.Secret))
	mac.Write(body)
	expectedSignature := hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(expectedSignature), []byte(signature)) == 1 {
		status = true
	}

	if !status {
		c.Status(fiber.StatusUnauthorized)
		return utils.SendCommonProtoJsonResponse(c, false, "can't verify provided information")
	}

	return c.Next()
}

func HandleVerifyToken(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")
	app := config.GetConfig()

	req := new(plugnmeet.VerifyTokenReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	cm := c.Locals("claims")
	if cm == nil {
		return utils.SendCommonProtobufResponse(c, false, "invalid request")
	}

	// check for duplicate join
	nts := natsservice.New(app)
	if status, err := nts.GetRoomUserStatus(roomId.(string), requestedUserId.(string)); err == nil {
		if status == natsservice.UserStatusOnline {
			return utils.SendCommonProtobufResponse(c, false, "notifications.room-disconnected-duplicate-entry")
		}
	}

	exist := nts.IsUserExistInBlockList(roomId.(string), requestedUserId.(string))
	if exist {
		return utils.SendCommonProtobufResponse(c, false, "notifications.you-are-blocked")
	}

	m := models.NewRoomModel(nil, nil, nil, nil)
	rr, _ := m.IsRoomActive(&plugnmeet.IsRoomActiveReq{
		RoomId: roomId.(string),
	})

	if !rr.GetIsActive() {
		// prevent joining if room status is not created or active
		return utils.SendCommonProtobufResponse(c, false, rr.Msg)
	}

	v := version.Version
	rId := roomId.(string)
	uId := requestedUserId.(string)
	natsSubjs := app.NatsInfo.Subjects
	res := &plugnmeet.VerifyTokenRes{
		Status:        true,
		Msg:           "token is valid",
		NatsWsUrls:    app.NatsInfo.NatsWSUrls,
		ServerVersion: &v,
		RoomId:        &rId,
		UserId:        &uId,
		NatsSubjects: &plugnmeet.NatsSubjects{
			SystemApiWorker: natsSubjs.SystemApiWorker,
			SystemJsWorker:  natsSubjs.SystemJsWorker,
			SystemPublic:    natsSubjs.SystemPublic,
			SystemPrivate:   natsSubjs.SystemPrivate,
			Chat:            natsSubjs.Chat,
			Whiteboard:      natsSubjs.Whiteboard,
			DataChannel:     natsSubjs.DataChannel,
		},
	}

	return utils.SendProtobufResponse(c, res)
}

func HandleVerifyHeaderToken(c *fiber.Ctx) error {
	authToken := c.Get("Authorization")
	m := models.NewAuthModel(nil, nil)

	errStatus := fiber.StatusUnauthorized
	path := c.Path()
	if strings.Contains(path, "file_upload") {
		errStatus = fiber.StatusBadRequest
	}

	if authToken == "" {
		_ = c.SendStatus(errStatus)
		return utils.SendCommonProtoJsonResponse(c, false, "Authorization header is missing")
	}

	claims, err := m.VerifyPlugNmeetAccessToken(authToken)
	if err != nil {
		_ = c.SendStatus(errStatus)
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	// we only need this during verify token
	// because it will return livekit token, if success
	if strings.Contains(c.Path(), "verifyToken") {
		c.Locals("claims", claims)
	}

	c.Locals("isAdmin", claims.IsAdmin)
	c.Locals("roomId", claims.RoomId)
	c.Locals("requestedUserId", claims.UserId)

	return c.Next()
}
