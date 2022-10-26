package controllers

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"github.com/gofiber/fiber/v2"
	"github.com/livekit/protocol/auth"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"github.com/mynaparrot/plugnmeet-server/version"
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
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"status": false,
			"msg":    "invalid API key",
		})
	}
	if signature == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"status": false,
			"msg":    "hash signature value required",
		})
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
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"status": false,
			"msg":    "can't verify provided information",
		})
	}

	return c.Next()
}

func HandleGenerateJoinToken(c *fiber.Ctx) error {
	req := new(plugnmeet.GenerateTokenReq)
	err := c.BodyParser(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	err = req.Validate()
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	if req.UserInfo == nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "UserInfo required",
		})
	}

	// don't generate token if user is blocked
	rs := models.NewRoomService()
	exist := rs.IsUserExistInBlockList(req.RoomId, req.UserInfo.UserId)
	if exist {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "this user is blocked to join this session",
		})
	}

	rm := models.NewRoomModel()
	ri, _ := rm.GetRoomInfo(req.RoomId, "", 1)
	if ri.Id == 0 {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "room is not active. create room first",
		})
	}

	m := models.NewAuthTokenModel()
	token, err := m.DoGenerateToken(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"status": true,
		"msg":    "success",
		"token":  token,
	})
}

func HandleVerifyToken(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")

	rs := models.NewRoomService()
	exist := rs.IsUserExistInBlockList(roomId.(string), requestedUserId.(string))
	if exist {
		return utils.SendCommonResponse(c, false, "notifications.you-are-blocked")
	}

	req := new(plugnmeet.VerifyTokenReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonResponse(c, false, err.Error())
	}

	cm := c.Locals("claims")
	if cm == nil {
		return utils.SendCommonResponse(c, false, "invalid request")
	}
	claims := cm.(*auth.ClaimGrants)
	// after usage, we can make it null as we don't need this value again.
	c.Locals("claims", nil)

	au := models.NewAuthTokenModel()
	token, err := au.GenerateLivekitToken(claims)
	if err != nil {
		return utils.SendCommonResponse(c, false, err.Error())
	}

	// if nil then assume production
	if req.IsProduction == nil {
		b := new(bool)
		*b = true
		req.IsProduction = b
	}

	livekitHost := strings.Replace(config.AppCnf.LivekitInfo.Host, "host.docker.internal", "localhost", 1) // without this you won't be able to connect
	v := version.Version
	res := &plugnmeet.VerifyTokenRes{
		Status:        true,
		Msg:           "token is valid",
		LivekitHost:   &livekitHost,
		Token:         &token,
		ServerVersion: &v,
	}

	if !*req.IsProduction {
		return utils.SendProtoResponse(c, res)
	}

	// if production then we'll check if room is active or not
	// if not active then we don't allow to join user
	// livekit also don't allow but throw 500 error which make confused to user.
	m := models.NewRoomAuthModel()
	status, msg := m.IsRoomActive(&plugnmeet.IsRoomActiveReq{
		RoomId: roomId.(string),
	})

	if !status {
		return utils.SendCommonResponse(c, status, msg)
	}

	res.Msg = msg
	return utils.SendProtoResponse(c, res)
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
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "Authorization header is missing",
		})
	}

	info := &models.ValidateTokenReq{
		Token: authToken,
	}

	claims, err := m.DoValidateToken(info, false)
	if err != nil {
		_ = c.SendStatus(errStatus)
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	// we only need this during verify token
	// because it will return livekit token, if success
	if strings.Contains(c.Path(), "verifyToken") {
		c.Locals("claims", claims)
	}

	c.Locals("isAdmin", claims.Video.RoomAdmin)
	c.Locals("roomId", claims.Video.Room)
	c.Locals("requestedUserId", claims.Identity)

	return c.Next()
}

// HandleRenewToken renew token only possible if it remains valid. This mean you'll require to renew it before expire.
func HandleRenewToken(c *fiber.Ctx) error {
	info := new(models.ValidateTokenReq)
	m := models.NewAuthTokenModel()

	err := c.BodyParser(info)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	if info.Token == "" || info.Sid == "" || info.RoomId == "" {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "missing required fields",
		})
	}

	token, err := m.DoRenewToken(info)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"status": true,
		"msg":    "token renewed",
		"token":  token,
	})
}
