package roomcontroller

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"github.com/bufbuild/protovalidate-go"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/auth"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/room"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/mynaparrot/plugnmeet-server/version"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"strings"
)

// HandleAuthHeaderCheck will check auth values
// It will accept 2 header values: API-KEY & HASH-SIGNATURE
// HASH-SIGNATURE will require to calculated hmac sha256 using
// body + Secret key
// Deprecated API-SECRET will be removed in next release
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
	if signature != "" {
		mac := hmac.New(sha256.New, []byte(config.GetConfig().Client.Secret))
		mac.Write(body)
		expectedSignature := hex.EncodeToString(mac.Sum(nil))
		if subtle.ConstantTimeCompare([]byte(expectedSignature), []byte(signature)) == 1 {
			status = true
		}
	}

	if !status {
		c.Status(fiber.StatusUnauthorized)
		return utils.SendCommonProtoJsonResponse(c, false, "can't verify provided information")
	}

	return c.Next()
}

func HandleGenerateJoinToken(c *fiber.Ctx) error {
	op := protojson.UnmarshalOptions{
		DiscardUnknown: true,
	}
	req := new(plugnmeet.GenerateTokenReq)
	err := op.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	v, err := protovalidate.New()
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, "failed to initialize validator: "+err.Error())
	}

	if err = v.Validate(req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	if req.UserInfo == nil {
		return utils.SendCommonProtoJsonResponse(c, false, "UserInfo required")
	}

	// don't generate token if user is blocked
	rs := redisservice.New(config.GetConfig().RDS)
	exist := rs.IsUserExistInBlockList(req.RoomId, req.UserInfo.UserId)
	if exist {
		return utils.SendCommonProtoJsonResponse(c, false, "this user is blocked to join this session")
	}

	ds := dbservice.New(config.GetConfig().ORM)
	ri, _ := ds.GetRoomInfoByRoomId(req.RoomId, 1)
	if ri == nil || ri.ID == 0 {
		return utils.SendCommonProtoJsonResponse(c, false, "room is not active. create room first")
	}

	m := roommodel.New(nil, nil, nil, nil)
	token, err := m.GetPNMJoinToken(req)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	r := &plugnmeet.GenerateTokenRes{
		Status: true,
		Msg:    "success",
		Token:  &token,
	}

	return utils.SendProtoJsonResponse(c, r)
}

func HandleVerifyToken(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")
	app := config.GetConfig()

	rs := redisservice.New(app.RDS)
	exist := rs.IsUserExistInBlockList(roomId.(string), requestedUserId.(string))
	if exist {
		return utils.SendCommonProtobufResponse(c, false, "notifications.you-are-blocked")
	}

	req := new(plugnmeet.VerifyTokenReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	cm := c.Locals("claims")
	if cm == nil {
		return utils.SendCommonProtobufResponse(c, false, "invalid request")
	}
	claims := cm.(*plugnmeet.PlugNmeetTokenClaims)
	// after usage, we can make it null as we don't need this value again.
	c.Locals("claims", nil)

	au := authmodel.New(nil, nil)
	token, err := au.GenerateLivekitToken(claims)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	// if nil then assume production
	if req.IsProduction == nil {
		b := new(bool)
		*b = true
		req.IsProduction = b
	}

	m := roommodel.New(nil, nil, nil, nil)
	rr, meta := m.IsRoomActive(&plugnmeet.IsRoomActiveReq{
		RoomId: roomId.(string),
	})

	// if production then we'll check if room is active or not
	// if not active then we don't allow to join user
	// livekit also don't allow but throw 500 error which make confused to user.
	if !rr.GetIsActive() && *req.IsProduction {
		return utils.SendProtoJsonResponse(c, rr)
	}

	livekitHost := strings.Replace(config.GetConfig().LivekitInfo.Host, "host.docker.internal", "localhost", 1) // without this you won't be able to connect
	v := version.Version
	rId := roomId.(string)
	uId := requestedUserId.(string)
	natsSubjs := app.NatsInfo.Subjects
	res := &plugnmeet.VerifyTokenRes{
		Status:        true,
		Msg:           "token is valid",
		LivekitHost:   &livekitHost,
		Token:         &token,
		ServerVersion: &v,
		EnabledE2Ee:   false,
		RoomId:        &rId,
		UserId:        &uId,
		NatsSubjects: &plugnmeet.NatsSubjects{
			SystemWorker:  natsSubjs.SystemWorker,
			SystemPublic:  natsSubjs.SystemPublic,
			SystemPrivate: natsSubjs.SystemPrivate,
			ChatPublic:    natsSubjs.ChatPublic,
			ChatPrivate:   natsSubjs.ChatPrivate,
			Whiteboard:    natsSubjs.Whiteboard,
		},
	}
	if rr.GetIsActive() && meta != nil {
		res.EnabledE2Ee = meta.RoomFeatures.EndToEndEncryptionFeatures.IsEnabled
	}

	return utils.SendProtobufResponse(c, res)
}

func HandleVerifyHeaderToken(c *fiber.Ctx) error {
	authToken := c.Get("Authorization")
	m := authmodel.New(nil, nil)

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

// HandleRenewToken renew token only possible if it remains valid. This mean you'll require to renew it before expire.
func HandleRenewToken(c *fiber.Ctx) error {
	info := new(authmodel.RenewTokenReq)
	m := authmodel.New(nil, nil)

	err := c.BodyParser(info)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	if info.Token == "" {
		return utils.SendCommonProtoJsonResponse(c, false, "missing required fields")
	}

	token, err := m.RenewPNMToken(info.Token)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	r := &plugnmeet.GenerateTokenRes{
		Status: true,
		Msg:    "token renewed",
		Token:  &token,
	}

	return utils.SendProtoJsonResponse(c, r)
}
