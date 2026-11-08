package controllers

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/version"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
	"google.golang.org/protobuf/proto"
)

// AuthController holds dependencies for auth-related handlers.
type AuthController struct {
	AppConfig   *config.AppConfig
	AuthModel   *models.AuthModel
	RoomModel   *models.RoomModel
	NatsService *natsservice.NatsService
	Logger      *logrus.Entry
}

type AuthControllerArgs struct {
	fx.In
	AppConfig   *config.AppConfig
	AuthModel   *models.AuthModel
	RoomModel   *models.RoomModel
	NatsService *natsservice.NatsService
	Logger      *logrus.Logger
}

// NewAuthController creates a new AuthController.
func NewAuthController(args AuthControllerArgs) *AuthController {
	return &AuthController{
		AppConfig:   args.AppConfig,
		AuthModel:   args.AuthModel,
		RoomModel:   args.RoomModel,
		NatsService: args.NatsService,
		Logger:      args.Logger.WithField("controller", "auth"),
	}
}

// HandleAuthHeaderCheck is a middleware to check API-KEY & HASH-SIGNATURE.
func (ac *AuthController) HandleAuthHeaderCheck(c fiber.Ctx) error {
	apiKey := c.Get("API-KEY")
	signature := c.Get("HASH-SIGNATURE")

	if apiKey == "" {
		return ac.sendAuthError(c, config.ErrAuthS2SApiKeyMissing, plugnmeet.StatusCode_MISSING_REQUIRED_PARAMETER)
	}
	if subtle.ConstantTimeCompare([]byte(apiKey), []byte(ac.AppConfig.Client.ApiKey)) != 1 {
		return ac.sendAuthError(c, config.ErrAuthS2SApiKeyInvalid, plugnmeet.StatusCode_INVALID_API_KEY)
	}

	if signature == "" {
		return ac.sendAuthError(c, config.ErrAuthS2SSignatureMissing, plugnmeet.StatusCode_MISSING_REQUIRED_PARAMETER)
	}

	mac := hmac.New(sha256.New, []byte(ac.AppConfig.Client.Secret))
	// we should use strings.Contains to match as different platform may add extra data
	if strings.Contains(c.Get(fiber.HeaderContentType), fiber.MIMEMultipartForm) {
		// For multipart/form-data, we sign the Room-Id header to ensure its integrity.
		roomId := c.Get(config.HeaderRoomId)
		if roomId == "" {
			// The client MUST send Room-Id for the signature contract to be met.
			return ac.sendAuthError(c, config.ErrAuthS2SRoomIdHeaderMissing, plugnmeet.StatusCode_MISSING_REQUIRED_PARAMETER)
		}
		mac.Write([]byte(roomId))
	} else {
		// For all other requests, we sign the raw body.
		mac.Write(c.Body())
	}

	expectedSignature := hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(expectedSignature), []byte(signature)) != 1 {
		return ac.sendAuthError(c, config.ErrAuthS2SSignatureInvalid, plugnmeet.StatusCode_INVALID_TOKEN_OR_SIGNATURE)
	}

	return c.Next()
}

// HandleVerifyHeaderToken is a middleware to verify the Authorization header token.
func (ac *AuthController) HandleVerifyHeaderToken(c fiber.Ctx) error {
	authToken := c.Get("Authorization")
	if authToken == "" {
		// fallback to form value
		authToken = c.FormValue("Authorization")
	}

	path := c.Path()

	if authToken == "" {
		return ac.sendAuthError(c, config.ErrAuthHeaderMissing, plugnmeet.StatusCode_MISSING_REQUIRED_PARAMETER)
	}

	claims, err := ac.AuthModel.VerifyPlugNmeetAccessToken(authToken, 0)
	if err != nil {
		return ac.sendAuthError(c, models.TokenVerifyError(err), plugnmeet.StatusCode_INVALID_TOKEN_OR_SIGNATURE)
	}

	// Let the "return to main room" route reach the handler even for an ended
	// breakout: the token is verified and the handler re-validates the parent.
	if strings.HasSuffix(path, "/backToMain") {
		c.Locals("isAdmin", claims.IsAdmin)
		c.Locals("roomId", claims.RoomId)
		c.Locals("requestedUserId", claims.UserId)
		c.Locals("clientType", claims.ClientType)
		return c.Next()
	}

	rf, err := ac.NatsService.GetRoomInfo(claims.RoomId)
	if err != nil {
		ac.logAuthFailure(claims.RoomId, claims.UserId, "failed to fetch room info", err)
		return ac.sendAuthError(c, config.ErrAuthUnexpected, plugnmeet.StatusCode_INTERNAL_SERVER_ERROR)
	}
	if rf == nil || rf.DbTableId == 0 || !ac.NatsService.IsRoomStatusActive(rf.Status) {
		return ac.sendAuthError(c, config.ErrAuthRoomNotActive, plugnmeet.StatusCode_ROOM_NOT_ACTIVE)
	}

	if ac.NatsService.IsUserExistInBlockList(claims.RoomId, claims.UserId) {
		return ac.sendAuthError(c, config.ErrAuthUserBlocked, plugnmeet.StatusCode_USER_BLOCKED)
	}

	if !strings.HasSuffix(path, "/verifyToken") && !strings.HasSuffix(path, "/getClientFiles") {
		status, err := ac.NatsService.GetRoomUserStatus(claims.RoomId, claims.UserId)
		if err != nil {
			ac.logAuthFailure(claims.RoomId, claims.UserId, "failed to fetch room user status", err)
			return ac.sendAuthError(c, config.ErrAuthUnexpected, plugnmeet.StatusCode_INTERNAL_SERVER_ERROR)
		}
		if status != natsservice.UserStatusOnline {
			return ac.sendAuthError(c, config.ErrAuthUserNotActive, plugnmeet.StatusCode_USER_NOT_ACTIVE)
		}
		meta, err := ac.NatsService.GetUserMetadataStruct(claims.RoomId, claims.UserId)
		if err != nil {
			ac.logAuthFailure(claims.RoomId, claims.UserId, "failed to fetch user metadata", err)
			return ac.sendAuthError(c, config.ErrAuthUnexpected, plugnmeet.StatusCode_INTERNAL_SERVER_ERROR)
		}
		if meta == nil {
			return ac.sendAuthError(c, config.ErrAuthUserNotActive, plugnmeet.StatusCode_USER_NOT_ACTIVE)
		}
		if meta.GetWaitForApproval() {
			return ac.sendAuthError(c, config.ErrAuthUserWaitApproval, plugnmeet.StatusCode_PERMISSION_DENIED)
		}
	}

	c.Locals("isAdmin", claims.IsAdmin)
	c.Locals("roomId", claims.RoomId)
	c.Locals("roomSid", rf.RoomSid)
	c.Locals("roomDbTableId", rf.DbTableId)
	c.Locals("requestedUserId", claims.UserId)
	c.Locals("clientType", claims.ClientType)

	return c.Next()
}

// sendAuthError sends a failed auth response. The HTTP status is non-2xx and
// derived from the status code, and the body carries the status code. The message
// comes from the caller: curated English for the server-to-server /auth
// endpoints, an i18n key for the browser facing /api endpoints.
func (ac *AuthController) sendAuthError(c fiber.Ctx, authErr error, statusCode plugnmeet.StatusCode) error {
	if c.Get(fiber.HeaderContentType) == "application/protobuf" {
		return utils.SendCommonErrorProtobufResponse(c, authErr.Error(), statusCode)
	}
	return utils.SendCommonErrorJsonResponse(c, authErr.Error(), statusCode)
}

// sendVerifyTokenError sends a failed verify token response. The endpoint always
// answers with a protobuf CommonResponse body.
func (ac *AuthController) sendVerifyTokenError(c fiber.Ctx, authErr error, statusCode plugnmeet.StatusCode) error {
	return utils.SendCommonErrorProtobufResponse(c, authErr.Error(), statusCode)
}

// logAuthFailure logs an operational auth failure once, at the failure site.
func (ac *AuthController) logAuthFailure(roomId, userId, msg string, err error) {
	ac.Logger.WithFields(logrus.Fields{
		"roomId": roomId,
		"userId": userId,
	}).WithError(err).Errorln(msg)
}

// HandleVerifyToken verifies a user's token before they join a room.
func (ac *AuthController) HandleVerifyToken(c fiber.Ctx) error {
	roomId := fiber.Locals[string](c, "roomId")
	requestedUserId := fiber.Locals[string](c, "requestedUserId")
	clientType := fiber.Locals[plugnmeet.ClientType](c, "clientType")

	req := new(plugnmeet.VerifyTokenReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		// An unparsable request is an invalid request from the client, not an
		// operational failure: report it without logging decoder internals.
		return ac.sendVerifyTokenError(c, config.ErrAuthInvalidRequest, plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	// check for duplicate join
	status, err := ac.NatsService.GetRoomUserStatus(roomId, requestedUserId)
	if err != nil {
		ac.logAuthFailure(roomId, requestedUserId, "failed to fetch room user status", err)
		return ac.sendVerifyTokenError(c, config.ErrAuthUnexpected, plugnmeet.StatusCode_INTERNAL_SERVER_ERROR)
	}
	if status == "" {
		return ac.sendVerifyTokenError(c, config.ErrAuthUserInfoNotFound, plugnmeet.StatusCode_USER_NOT_FOUND)
	} else if status == natsservice.UserStatusOnline {
		return ac.sendVerifyTokenError(c, config.ErrAuthDuplicateEntry, plugnmeet.StatusCode_CONFLICT)
	} else if status == natsservice.UserStatusRemoved {
		return ac.sendVerifyTokenError(c, config.ErrAuthUserRemoved, plugnmeet.StatusCode_USER_NOT_ACTIVE)
	}

	exist := ac.NatsService.IsUserExistInBlockList(roomId, requestedUserId)
	if exist {
		return ac.sendVerifyTokenError(c, config.ErrAuthUserBlocked, plugnmeet.StatusCode_USER_BLOCKED)
	}

	rr, rInfo, meta := ac.RoomModel.IsRoomActive(&plugnmeet.IsRoomActiveReq{
		RoomId: roomId,
	})

	if !rr.GetIsActive() {
		return ac.sendVerifyTokenError(c, config.ErrAuthRoomNotActive, plugnmeet.StatusCode_ROOM_NOT_ACTIVE)
	}

	// rInfo and meta are guaranteed to be non-nil if IsActive is true.
	if rInfo.MaxParticipants > 0 {
		onlineUsers, err := ac.NatsService.GetOnlineUsersId(roomId)
		if err != nil {
			ac.logAuthFailure(roomId, requestedUserId, "failed to fetch online users", err)
			return ac.sendVerifyTokenError(c, config.ErrAuthUnexpected, plugnmeet.StatusCode_INTERNAL_SERVER_ERROR)
		}
		if int64(len(onlineUsers)) >= int64(rInfo.MaxParticipants) {
			return ac.sendVerifyTokenError(c, config.ErrAuthMaxParticipants, plugnmeet.StatusCode_CONFLICT)
		}
	}

	enabledSelfInsertEncryptionKey := false
	if meta.RoomFeatures.EndToEndEncryptionFeatures.IsEnabled {
		enabledSelfInsertEncryptionKey = meta.RoomFeatures.EndToEndEncryptionFeatures.EnabledSelfInsertEncryptionKey
	}

	natsSubjs := ac.AppConfig.NatsInfo.Subjects
	res := &plugnmeet.VerifyTokenRes{
		Status:         true,
		Msg:            config.AuthMsgTokenValid,
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
		ClientType:                     clientType,
	}

	return utils.SendProtobufResponse(c, res)
}
