package controllers

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/gofiber/fiber/v3"
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
func (ac *AuthController) HandleAuthHeaderCheck(c fiber.Ctx) error {
	apiKey := c.Get("API-KEY")
	signature := c.Get("HASH-SIGNATURE")

	if apiKey == "" {
		c.Status(fiber.StatusUnauthorized)
		return ac.sendVerificationRes(c, false, "Missing API-KEY header.", plugnmeet.StatusCode_MISSING_REQUIRED_PARAMETER)
	}
	if apiKey != ac.AppConfig.Client.ApiKey {
		c.Status(fiber.StatusUnauthorized)
		return ac.sendVerificationRes(c, false, "Invalid API key provided.", plugnmeet.StatusCode_INVALID_API_KEY)
	}

	if signature == "" {
		c.Status(fiber.StatusUnauthorized)
		return ac.sendVerificationRes(c, false, "Missing HASH-SIGNATURE header.", plugnmeet.StatusCode_MISSING_REQUIRED_PARAMETER)
	}

	mac := hmac.New(sha256.New, []byte(ac.AppConfig.Client.Secret))
	if strings.Contains(c.Get("Content-type"), "multipart/form-data") {
		// For multipart/form-data, we sign the Room-Id header to ensure its integrity.
		roomId := c.Get("Room-Id")
		if roomId == "" {
			// The client MUST send Room-Id for the signature contract to be met.
			c.Status(fiber.StatusUnauthorized)
			return ac.sendVerificationRes(c, false, "Missing Room-Id header for multipart request.", plugnmeet.StatusCode_MISSING_REQUIRED_PARAMETER)
		}
		mac.Write([]byte(roomId))
	} else {
		// For all other requests, we sign the raw body.
		mac.Write(c.Body())
	}

	expectedSignature := hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(expectedSignature), []byte(signature)) != 1 {
		c.Status(fiber.StatusUnauthorized)
		return ac.sendVerificationRes(c, false, "Failed to verify provided authentication details.", plugnmeet.StatusCode_INVALID_TOKEN_OR_SIGNATURE)
	}

	return c.Next()
}

// HandleVerifyHeaderToken is a middleware to verify the Authorization header token.
func (ac *AuthController) HandleVerifyHeaderToken(c fiber.Ctx) error {
	authToken := c.Get("Authorization")

	errStatus := fiber.StatusUnauthorized
	path := c.Path()
	if strings.Contains(path, "fileUpload") {
		// mostly to handle resumable.js path
		errStatus = fiber.StatusBadRequest
	}

	if authToken == "" {
		_ = c.SendStatus(errStatus)
		return ac.sendVerificationRes(c, false, "notifications.auth-header-missing", plugnmeet.StatusCode_MISSING_REQUIRED_PARAMETER)
	}

	claims, err := ac.AuthModel.VerifyPlugNmeetAccessToken(authToken, 0)
	if err != nil {
		_ = c.SendStatus(errStatus)
		errMsg := "notifications.invalid-token"
		if errors.Is(err, jwt.ErrExpired) {
			errMsg = "notifications.token-expired"
		}
		return ac.sendVerificationRes(c, false, errMsg, plugnmeet.StatusCode_INVALID_TOKEN_OR_SIGNATURE)
	}

	rf, err := ac.NatsService.GetRoomInfo(claims.RoomId)
	if err != nil {
		_ = c.SendStatus(errStatus)
		return ac.sendVerificationRes(c, false, err.Error(), plugnmeet.StatusCode_INTERNAL_SERVER_ERROR)
	}
	if rf == nil || rf.DbTableId == 0 || !ac.NatsService.IsRoomStatusActive(rf.Status) {
		_ = c.SendStatus(errStatus)
		return ac.sendVerificationRes(c, false, "notifications.room-not-active", plugnmeet.StatusCode_ROOM_NOT_FOUND)
	}

	c.Locals("isAdmin", claims.IsAdmin)
	c.Locals("roomId", claims.RoomId)
	c.Locals("roomSid", rf.RoomSid)
	c.Locals("roomDbTableId", rf.DbTableId)
	c.Locals("requestedUserId", claims.UserId)

	return c.Next()
}

func (ac *AuthController) sendVerificationRes(c fiber.Ctx, s bool, m string, statusCode plugnmeet.StatusCode) error {
	cType := c.Get(fiber.HeaderContentType)
	if cType == fiber.MIMEApplicationJSON {
		return utils.SendCommonProtoJsonResponse(c, s, m, statusCode)
	}
	return utils.SendCommonProtobufResponse(c, s, m)
}

// HandleVerifyToken verifies a user's token before they join a room.
func (ac *AuthController) HandleVerifyToken(c fiber.Ctx) error {
	roomId := fiber.Locals[string](c, "roomId")
	requestedUserId := fiber.Locals[string](c, "requestedUserId")

	req := new(plugnmeet.VerifyTokenReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	// check for duplicate join
	status, err := ac.NatsService.GetRoomUserStatus(roomId, requestedUserId)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}
	if status == "" {
		return utils.SendCommonProtobufResponse(c, false, "notifications.user-info-not-found")
	} else if status == natsservice.UserStatusOnline {
		return utils.SendCommonProtobufResponse(c, false, "notifications.room-disconnected-duplicate-entry")
	}

	exist := ac.NatsService.IsUserExistInBlockList(roomId, requestedUserId)
	if exist {
		return utils.SendCommonProtobufResponse(c, false, "notifications.you-are-blocked")
	}

	rr, rInfo, meta := ac.RoomModel.IsRoomActive(&plugnmeet.IsRoomActiveReq{
		RoomId: roomId,
	})

	if !rr.GetIsActive() {
		return utils.SendCommonProtobufResponse(c, false, "notifications.room-not-active")
	}

	// rInfo and meta are guaranteed to be non-nil if IsActive is true.
	if rInfo.MaxParticipants > 0 {
		onlineUsers, err := ac.NatsService.GetOnlineUsersId(roomId)
		if err != nil {
			return utils.SendCommonProtobufResponse(c, false, err.Error())
		}
		if int64(len(onlineUsers)) >= int64(rInfo.MaxParticipants) {
			return utils.SendCommonProtobufResponse(c, false, "notifications.max-num-participates-exceeded")
		}
	}

	enabledSelfInsertEncryptionKey := false
	if meta.RoomFeatures.EndToEndEncryptionFeatures.IsEnabled {
		enabledSelfInsertEncryptionKey = meta.RoomFeatures.EndToEndEncryptionFeatures.EnabledSelfInsertEncryptionKey
	}

	natsSubjs := ac.AppConfig.NatsInfo.Subjects
	res := &plugnmeet.VerifyTokenRes{
		Status:         true,
		Msg:            "token is valid",
		NatsWsUrls:     ac.AppConfig.NatsInfo.NatsWSUrls,
		ServerVersion:  new(version.Version),
		RoomId:         &roomId,
		UserId:         &requestedUserId,
		RoomStreamName: &ac.AppConfig.NatsInfo.RoomStreamName,
		NatsSubjects: &plugnmeet.NatsSubjects{
			SystemApiWorker:  natsSubjs.SystemApiWorker,
			SystemJsWorker:   natsSubjs.SystemJsWorker,
			SystemCoreWorker: natsSubjs.SystemCoreWorker,
			SystemPublic:     natsSubjs.SystemPublic,
			SystemPrivate:    natsSubjs.SystemPrivate,
			Chat:             natsSubjs.Chat,
			Whiteboard:       natsSubjs.Whiteboard,
			DataChannel:      natsSubjs.DataChannel,
		},
		EnabledSelfInsertEncryptionKey: &enabledSelfInsertEncryptionKey,
	}

	return utils.SendProtobufResponse(c, res)
}
