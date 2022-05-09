package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugNmeet/internal/config"
	"github.com/mynaparrot/plugNmeet/internal/models"
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
	req := new(models.GenTokenReq)
	m := models.NewAuthTokenModel()

	err := c.BodyParser(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}
	check := m.Validation(req)
	if len(check) > 0 {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    check,
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

	// if nil then assume production
	if req.IsProduction == nil {
		b := new(bool)
		*b = true
		req.IsProduction = b
	}

	if !*req.IsProduction {
		return c.JSON(fiber.Map{
			"status": true,
			"msg":    "token is valid",
		})
	}

	// if production then we'll check if room is active or not
	// if not active then we don't allow to join user
	// livekit also don't allow but throw 500 error which make confused to user.
	roomId := c.Locals("roomId")
	m := models.NewRoomAuthModel()
	status, msg := m.IsRoomActive(&models.IsRoomActiveReq{
		RoomId: roomId.(string),
	})

	return c.JSON(fiber.Map{
		"status": status,
		"msg":    msg,
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

	claims, err := m.DoValidateToken(info)
	if err != nil {
		_ = c.SendStatus(errStatus)
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	// TO-DO verify if meeting is running or not.
	// If not running then we'll send error response.
	// new livekit server won't allow creat room by join.

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
