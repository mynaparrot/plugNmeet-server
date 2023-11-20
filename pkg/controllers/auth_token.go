package controllers

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
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
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

	if apiKey != config.AppCnf.Client.ApiKey {
		c.Status(fiber.StatusUnauthorized)
		return utils.SendCommonProtoJsonResponse(c, false, "invalid API key")
	}
	if signature == "" {
		c.Status(fiber.StatusUnauthorized)
		return utils.SendCommonProtoJsonResponse(c, false, "hash signature value required")
	}

	status := false
	if signature != "" {
		mac := hmac.New(sha256.New, []byte(config.AppCnf.Client.Secret))
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
		utils.SendCommonProtoJsonResponse(c, false, "failed to initialize validator: "+err.Error())
	}

	if err = v.Validate(req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	if req.UserInfo == nil {
		return utils.SendCommonProtoJsonResponse(c, false, "UserInfo required")
	}

	// don't generate token if user is blocked
	rs := models.NewRoomService()
	exist := rs.IsUserExistInBlockList(req.RoomId, req.UserInfo.UserId)
	if exist {
		return utils.SendCommonProtoJsonResponse(c, false, "this user is blocked to join this session")
	}

	rm := models.NewRoomModel()
	ri, _ := rm.GetRoomInfo(req.RoomId, "", 1)
	if ri.Id == 0 {
		return utils.SendCommonProtoJsonResponse(c, false, "room is not active. create room first")
	}

	m := models.NewAuthTokenModel()
	token, err := m.GeneratePlugNmeetAccessToken(req)
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

	rs := models.NewRoomService()
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

	au := models.NewAuthTokenModel()
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

	m := models.NewRoomAuthModel()
	status, msg, meta := m.IsRoomActive(&plugnmeet.IsRoomActiveReq{
		RoomId: roomId.(string),
	})

	// if production then we'll check if room is active or not
	// if not active then we don't allow to join user
	// livekit also don't allow but throw 500 error which make confused to user.
	if !status && *req.IsProduction {
		return utils.SendCommonProtobufResponse(c, status, msg)
	}

	livekitHost := strings.Replace(config.AppCnf.LivekitInfo.Host, "host.docker.internal", "localhost", 1) // without this you won't be able to connect
	v := version.Version
	res := &plugnmeet.VerifyTokenRes{
		Status:        true,
		Msg:           "token is valid",
		LivekitHost:   &livekitHost,
		Token:         &token,
		ServerVersion: &v,
		EnabledE2Ee:   false,
	}
	if status && meta != nil {
		res.EnabledE2Ee = meta.RoomFeatures.EndToEndEncryptionFeatures.IsEnabled
	}

	return utils.SendProtobufResponse(c, res)
}

func HandleVerifyHeaderToken(c *fiber.Ctx) error {
	authToken := c.Get("Authorization")
	m := models.NewAuthTokenModel()

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
	info := new(models.RenewTokenReq)
	m := models.NewAuthTokenModel()

	err := c.BodyParser(info)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	if info.Token == "" {
		return utils.SendCommonProtoJsonResponse(c, false, "missing required fields")
	}

	token, err := m.DoRenewPlugNmeetToken(info.Token)
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
