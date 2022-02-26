package controllers

import (
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugNmeet/internal/config"
	"github.com/mynaparrot/plugNmeet/internal/models"
	"gopkg.in/square/go-jose.v2/jwt"
	"os"
	"strconv"
	"strings"
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

	tok, err := jwt.ParseSigned(token)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	out := jwt.Claims{}
	if err = tok.UnsafeClaimsWithoutVerification(&out); err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	if config.AppCnf.Client.ApiKey != out.Issuer {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "invalid token issuer",
		})
	}

	file := fmt.Sprintf("%s/%s", config.AppCnf.RecorderInfo.RecordingFilesPath, out.Subject)

	fileInfo, err := os.Lstat(file)
	if err != nil {
		_ = c.SendStatus(fiber.StatusNotFound)
		ms := strings.SplitN(err.Error(), "/", -1)

		return c.JSON(fiber.Map{
			"status": false,
			"msg":    ms[4],
		})
	}

	c.Set("Content-Disposition", "attachment; filename="+strconv.Quote(fileInfo.Name()))
	c.Set("Content-Type", "application/octet-stream")
	return c.SendFile(file)
}
