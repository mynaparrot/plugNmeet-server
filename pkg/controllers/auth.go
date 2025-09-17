package controllers

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/version"
	"google.golang.org/protobuf/proto"
)

// AuthController holds dependencies for auth-related handlers.
type AuthController struct {
	AppConfig   *config.AppConfig
	AuthModel   *models.AuthModel
	RoomModel   *models.RoomModel
	NatsService *natsservice.NatsService
}

// NewAuthController creates a new AuthController.
func NewAuthController(config *config.AppConfig, natsService *natsservice.NatsService, authModel *models.AuthModel, roomModel *models.RoomModel) *AuthController {
	return &AuthController{
		AppConfig:   config,
		AuthModel:   authModel,
		RoomModel:   roomModel,
		NatsService: natsService,
	}
}

// HandleAuthHeaderCheck is a middleware to check API-KEY & HASH-SIGNATURE.
func (ac *AuthController) HandleAuthHeaderCheck(c *fiber.Ctx) error {
	apiKey := c.Get("API-KEY", "")
	signature := c.Get("HASH-SIGNATURE", "")
	body := c.Body()

	if apiKey != ac.AppConfig.Client.ApiKey {
		c.Status(fiber.StatusUnauthorized)
		return utils.SendCommonProtoJsonResponse(c, false, "invalid API key")
	}
	if signature == "" {
		c.Status(fiber.StatusUnauthorized)
		return utils.SendCommonProtoJsonResponse(c, false, "hash signature value required")
	}

	mac := hmac.New(sha256.New, []byte(ac.AppConfig.Client.Secret))
	mac.Write(body)
	expectedSignature := hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(expectedSignature), []byte(signature)) != 1 {
		c.Status(fiber.StatusUnauthorized)
		return utils.SendCommonProtoJsonResponse(c, false, "can't verify provided information")
	}

	return c.Next()
}

// HandleVerifyToken verifies a user's token before they join a room.
func (ac *AuthController) HandleVerifyToken(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")

	req := new(plugnmeet.VerifyTokenReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	// check for duplicate join
	status, err := ac.NatsService.GetRoomUserStatus(roomId.(string), requestedUserId.(string))
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}
	if status == natsservice.UserStatusOnline {
		return utils.SendCommonProtobufResponse(c, false, "notifications.room-disconnected-duplicate-entry")
	}

	exist := ac.NatsService.IsUserExistInBlockList(roomId.(string), requestedUserId.(string))
	if exist {
		return utils.SendCommonProtobufResponse(c, false, "notifications.you-are-blocked")
	}

	rr, roomDbInfo, rInfo, meta := ac.RoomModel.IsRoomActive(c.UserContext(), &plugnmeet.IsRoomActiveReq{
		RoomId: roomId.(string),
	})

	if !rr.GetIsActive() {
		return utils.SendCommonProtobufResponse(c, false, rr.Msg)
	}
	if rInfo.MaxParticipants > 0 && roomDbInfo.JoinedParticipants >= int64(rInfo.MaxParticipants) {
		return utils.SendCommonProtobufResponse(c, false, "notifications.max-num-participates-exceeded")
	}

	v := version.Version
	rId := roomId.(string)
	uId := requestedUserId.(string)
	natsSubjs := ac.AppConfig.NatsInfo.Subjects
	res := &plugnmeet.VerifyTokenRes{
		Status:        true,
		Msg:           "token is valid",
		NatsWsUrls:    ac.AppConfig.NatsInfo.NatsWSUrls,
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
		EnabledSelfInsertEncryptionKey: &meta.GetRoomFeatures().GetEndToEndEncryptionFeatures().EnabledSelfInsertEncryptionKey,
	}

	return utils.SendProtobufResponse(c, res)
}

// HandleVerifyHeaderToken is a middleware to verify the Authorization header token.
func (ac *AuthController) HandleVerifyHeaderToken(c *fiber.Ctx) error {
	authToken := c.Get("Authorization")

	errStatus := fiber.StatusUnauthorized
	path := c.Path()
	if strings.Contains(path, "file_upload") {
		errStatus = fiber.StatusBadRequest
	}

	if authToken == "" {
		_ = c.SendStatus(errStatus)
		return utils.SendCommonProtoJsonResponse(c, false, "Authorization header is missing")
	}

	claims, err := ac.AuthModel.VerifyPlugNmeetAccessToken(authToken, true)
	if err != nil {
		_ = c.SendStatus(errStatus)
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	c.Locals("isAdmin", claims.IsAdmin)
	c.Locals("roomId", claims.RoomId)
	c.Locals("requestedUserId", claims.UserId)

	return c.Next()
}
