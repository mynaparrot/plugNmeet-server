package controllers

import (
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugNmeet/internal/models"
)

func HandleLTIV1Landing(c *fiber.Ctx) error {
	b := make([]byte, len(c.Body()))
	copy(b, c.Body())

	if len(b) == 0 {
		return c.Status(fiber.StatusUnauthorized).SendString("empty body")
	}

	signingURL := fmt.Sprintf("%v://%v%v", c.Protocol(), c.Hostname(), c.OriginalURL())
	m := models.NewLTIV1Model()
	err := m.Landing(c, string(b), signingURL)
	if err != nil {
		return err
	}

	return nil
}

func HandleLTIV1VerifyHeaderToken(c *fiber.Ctx) error {
	authToken := c.Get("Authorization")

	m := models.NewLTIV1Model()
	auth, err := m.LTIV1VerifyHeaderToken(authToken)
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"status": false,
			"msg":    "Authorization header is missing",
		})
	}

	c.Locals("roomId", auth.RoomId)
	c.Locals("roomTitle", auth.RoomTitle)

	c.Locals("userId", auth.UserId)
	c.Locals("name", auth.Name)
	c.Locals("isAdmin", auth.IsAdmin)

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

	token, err := m.LTIV1JoinRoom(&models.LtiClaims{
		UserId:    c.Locals("userId").(string),
		Name:      c.Locals("name").(string),
		IsAdmin:   c.Locals("isAdmin").(bool),
		RoomId:    c.Locals("roomId").(string),
		RoomTitle: c.Locals("roomTitle").(string),
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
