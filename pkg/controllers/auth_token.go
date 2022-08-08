package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/livekit/protocol/auth"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"strings"
)

func HandleAuthHeaderCheck(c *fiber.Ctx) error {
	apiKey := c.Get("API-KEY")
	secret := c.Get("API-SECRET")

	if apiKey == "" || secret == "" {
		_ = c.SendStatus(fiber.StatusUnauthorized)
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "Auth header information are missing",
		})
	}

	if apiKey != config.AppCnf.Client.ApiKey || secret != config.AppCnf.Client.Secret {
		_ = c.SendStatus(fiber.StatusUnauthorized)
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "Auth header information didn't match",
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
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "notifications.you-are-blocked",
		})
	}

	req := new(struct {
		IsProduction *bool `json:"is_production,omitempty"`
	})

	err := c.BodyParser(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	cm := c.Locals("claims")
	// after usage, we can make it null as we don't need this value again.
	c.Locals("claims", nil)
	claims := cm.(*auth.ClaimGrants)

	au := models.NewAuthTokenModel()
	token, err := au.GenerateLivekitToken(claims)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	// if nil then assume production
	if req.IsProduction == nil {
		b := new(bool)
		*b = true
		req.IsProduction = b
	}

	livekitHost := strings.Replace(config.AppCnf.LivekitInfo.Host, "host.docker.internal", "localhost", 1) // without this you won't be able to connect

	if !*req.IsProduction {
		return c.JSON(fiber.Map{
			"status":       true,
			"msg":          "token is valid",
			"livekit_host": livekitHost,
			"token":        token,
		})
	}

	// if production then we'll check if room is active or not
	// if not active then we don't allow to join user
	// livekit also don't allow but throw 500 error which make confused to user.
	m := models.NewRoomAuthModel()
	status, msg := m.IsRoomActive(&plugnmeet.IsRoomActiveReq{
		RoomId: roomId.(string),
	})

	if !status {
		return c.JSON(fiber.Map{
			"status": status,
			"msg":    msg,
		})
	}

	return c.JSON(fiber.Map{
		"status":       status,
		"msg":          msg,
		"livekit_host": livekitHost,
		"token":        token,
	})
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
