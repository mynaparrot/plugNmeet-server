package controllers

import (
	"fmt"
	"github.com/goccy/go-json"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"strings"
)

func HandleLTIV1Landing(c *fiber.Ctx) error {
	b := make([]byte, len(c.Body()))
	copy(b, c.Body())

	if len(b) == 0 {
		return c.Status(fiber.StatusUnauthorized).SendString("empty body")
	}

	proto := c.Get("X-Forwarded-Proto", "https")
	signingURL := fmt.Sprintf("%s://%s%s", proto, c.Hostname(), c.Path())

	if strings.Contains(c.Hostname(), "localhost") {
		// fallback to default, if localhost
		signingURL = config.AppCnf.LtiInfo.V1ToolUrl
	}

	m := models.NewLTIV1Model()
	err := m.LTIV1Landing(c, string(b), signingURL)
	if err != nil {
		return err
	}

	return nil
}

func HandleLTIV1GETREQUEST(c *fiber.Ctx) error {
	return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
		"status": false,
		"msg":    "please use POST request",
	})
}

func HandleLTIV1VerifyHeaderToken(c *fiber.Ctx) error {
	authToken := c.Get("Authorization")
	if authToken == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"status": false,
			"msg":    "Authorization header is missing",
		})
	}

	m := models.NewLTIV1Model()
	auth, err := m.LTIV1VerifyHeaderToken(authToken)
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"status": false,
			"msg":    "invalid authorization header",
		})
	}

	c.Locals("roomId", auth.RoomId)
	c.Locals("roomTitle", auth.RoomTitle)

	c.Locals("userId", auth.UserId)
	c.Locals("name", auth.Name)
	c.Locals("isAdmin", auth.IsAdmin)

	if auth.LtiCustomParameters != nil {
		customParams, err := json.Marshal(auth.LtiCustomParameters)
		if err == nil {
			c.Locals("customParams", customParams)
		}
	}

	return c.Next()
}

func HandleLTIV1IsRoomActive(c *fiber.Ctx) error {
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

func HandleLTIV1JoinRoom(c *fiber.Ctx) error {
	m := models.NewLTIV1Model()
	customParams := c.Locals("customParams").([]byte)

	claim := &models.LtiClaims{
		UserId:    c.Locals("userId").(string),
		Name:      c.Locals("name").(string),
		IsAdmin:   c.Locals("isAdmin").(bool),
		RoomId:    c.Locals("roomId").(string),
		RoomTitle: c.Locals("roomTitle").(string),
	}

	if len(customParams) > 0 {
		p := new(models.LtiCustomParameters)
		err := json.Unmarshal(customParams, p)
		if err == nil {
			claim.LtiCustomParameters = p
		}
	}

	token, err := m.LTIV1JoinRoom(claim)
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

func HandleLTIV1EndRoom(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	isAdmin := c.Locals("isAdmin").(bool)

	if !isAdmin {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "only admin can perform this",
		})
	}

	m := models.NewRoomAuthModel()
	status, msg := m.EndRoom(&models.RoomEndReq{
		RoomId: roomId.(string),
	})

	return c.JSON(fiber.Map{
		"status": status,
		"msg":    msg,
	})
}

func HandleLTIV1FetchRecordings(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")

	req := new(models.LTIV1FetchRecordingsReq)
	err := c.BodyParser(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	m := models.NewRecordingAuth()
	result, err := m.FetchRecordings(&models.FetchRecordingsReq{
		RoomIds: []string{roomId.(string)},
		From:    req.From,
		Limit:   req.Limit,
		OrderBy: req.OrderBy,
	})

	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"status": true,
		"msg":    "success",
		"result": result,
	})
}

func HandleLTIV1GetRecordingDownloadToken(c *fiber.Ctx) error {
	req := new(models.GetDownloadTokenReq)
	err := c.BodyParser(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	m := models.NewRecordingAuth()
	token, err := m.GetDownloadToken(req)
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

func HandleLTIV1DeleteRecordings(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin").(bool)

	if !isAdmin {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "only admin can perform this",
		})
	}

	req := new(models.DeleteRecordingReq)
	err := c.BodyParser(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	m := models.NewRecordingAuth()
	err = m.DeleteRecording(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"status": true,
		"msg":    "success",
	})
}
