package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugNmeet/pkg/config"
	"github.com/mynaparrot/plugNmeet/pkg/models"
)

func HandleFetchRecordings(c *fiber.Ctx) error {
	req := new(models.FetchRecordingsReq)
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

	m := models.NewRecordingAuth()
	result, err := m.FetchRecordings(req)

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

func HandleDeleteRecording(c *fiber.Ctx) error {
	req := new(models.DeleteRecordingReq)
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

func HandleGetDownloadToken(c *fiber.Ctx) error {
	req := new(models.GetDownloadTokenReq)
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

func HandleDownloadRecording(c *fiber.Ctx) error {
	token := c.Params("token")

	if len(token) == 0 {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "token require or invalid url",
		})
	}

	m := models.NewRecordingAuth()
	file, err := m.VerifyRecordingToken(token)

	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	c.Attachment(file)
	return c.SendFile(file, true)
}
