package controllers

import (
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugNmeet/internal/config"
	"github.com/mynaparrot/plugNmeet/internal/models"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// HandleChatFileUpload method can only be use if you are using resumable.js as your frontend.
// Library link: https://github.com/23/resumable.js
func HandleChatFileUpload(c *fiber.Ctx) error {
	req := new(models.ManageFile)
	err := c.QueryParser(req)
	if err != nil {
		_ = c.SendStatus(fiber.StatusBadRequest)
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	check := config.AppCnf.DoValidateReq(req)
	if len(check) > 0 {
		_ = c.SendStatus(fiber.StatusBadRequest)
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    check,
		})
	}

	m := models.NewManageFileModel(req)
	err = m.CommonValidation(c)
	if err != nil {
		_ = c.SendStatus(fiber.StatusBadRequest)
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	var filePath string
	var fileName string
	if req.Resumable {
		filePath, fileName, err = m.ResumableFileUpload(c)
		if err != nil {
			return c.JSON(fiber.Map{
				"status": false,
				"msg":    err.Error(),
			})
		}

		if filePath == "part_uploaded" {
			_ = c.SendStatus(fiber.StatusOK)
			return c.SendString(filePath)
		}
	}

	return c.JSON(fiber.Map{
		"status":   true,
		"msg":      "file uploaded successfully",
		"filePath": filePath,
		"fileName": fileName,
	})
}

func HandleDownloadChatFile(c *fiber.Ctx) error {
	sid := c.Params("sid")
	fileName := c.Params("fileName")
	fileName, _ = url.QueryUnescape(fileName)

	file := fmt.Sprintf("%s/%s/%s", config.AppCnf.UploadFileSettings.Path, sid, fileName)
	fileInfo, err := os.Lstat(file)
	if err != nil {
		_ = c.SendStatus(fiber.StatusNotFound)
		ms := strings.SplitN(err.Error(), "/", -1)
		return c.SendString(ms[3])
	}

	c.Set("Content-Disposition", "attachment; filename="+strconv.Quote(fileInfo.Name()))
	c.Set("Content-Type", "application/octet-stream")
	return c.SendFile(file)
}
