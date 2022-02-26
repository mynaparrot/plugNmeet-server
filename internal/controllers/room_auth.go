package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugNmeet/internal/config"
	"github.com/mynaparrot/plugNmeet/internal/models"
)

func HandleRoomCreate(c *fiber.Ctx) error {
	req := new(models.RoomCreateReq)
	err := c.BodyParser(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}
	check := config.AppCnf.DoValidateReq(req)
	if len(check) > 0 {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    check,
		})
	}

	m := models.NewRoomAuthModel()
	status, msg, room := m.CreateRoom(req)

	return c.JSON(fiber.Map{
		"status":   status,
		"msg":      msg,
		"roomInfo": room,
	})
}

func HandleIsRoomActive(c *fiber.Ctx) error {
	req := new(models.IsRoomActiveReq)
	err := c.BodyParser(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}
	check := config.AppCnf.DoValidateReq(req)
	if len(check) > 0 {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    check,
		})
	}

	m := models.NewRoomAuthModel()
	status, msg := m.IsRoomActive(req)

	return c.JSON(fiber.Map{
		"status": status,
		"msg":    msg,
	})
}

func HandleGetActiveRoomInfo(c *fiber.Ctx) error {
	req := new(models.IsRoomActiveReq)
	err := c.BodyParser(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}
	check := config.AppCnf.DoValidateReq(req)
	if len(check) > 0 {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    check,
		})
	}
	m := models.NewRoomAuthModel()
	status, msg, res := m.GetActiveRoomInfo(req)

	return c.JSON(fiber.Map{
		"status": status,
		"msg":    msg,
		"room":   res,
	})
}

func HandleGetActiveRoomsInfo(c *fiber.Ctx) error {
	m := models.NewRoomAuthModel()
	status, msg, res := m.GetActiveRoomsInfo()

	return c.JSON(fiber.Map{
		"status": status,
		"msg":    msg,
		"rooms":  res,
	})
}

func HandleEndRoom(c *fiber.Ctx) error {
	req := new(models.RoomEndReq)
	err := c.BodyParser(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}
	check := config.AppCnf.DoValidateReq(req)
	if len(check) > 0 {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    check,
		})
	}

	m := models.NewRoomAuthModel()
	status, msg := m.EndRoom(req)

	return c.JSON(fiber.Map{
		"status": status,
		"msg":    msg,
	})
}

func HandleEndRoomForAPI(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")

	if !isAdmin.(bool) {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "only admin can perform this task",
		})
	}

	req := new(models.RoomEndReq)
	err := c.BodyParser(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	check := config.AppCnf.DoValidateReq(req)
	if len(check) > 0 {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    check,
		})
	}

	if roomId != req.RoomId {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "requested roomId & token roomId mismatched",
		})
	}

	m := models.NewRoomAuthModel()
	status, msg := m.EndRoom(req)

	return c.JSON(fiber.Map{
		"status": status,
		"msg":    msg,
	})
}
