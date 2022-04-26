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

	if req.Resumable {
		res, err := m.ResumableFileUpload(c)
		if err != nil {
			return c.JSON(fiber.Map{
				"status": false,
				"msg":    err.Error(),
			})
		}

		if res.FilePath == "part_uploaded" {
			_ = c.SendStatus(fiber.StatusOK)
			return c.SendString(res.FilePath)
		} else {
			return c.JSON(fiber.Map{
				"status":        true,
				"msg":           "file uploaded successfully",
				"filePath":      res.FilePath,
				"fileName":      res.FileName,
				"fileExtension": res.FileExtension,
				"fileMimeType":  res.FileMimeType,
			})
		}
	}

	return c.JSON(fiber.Map{
		"status": false,
		"msg":    "upload method not supported",
	})
}

func HandleDownloadUploadedFile(c *fiber.Ctx) error {
	sid := c.Params("sid")
	otherParts := c.Params("*")
	otherParts, _ = url.QueryUnescape(otherParts)

	file := fmt.Sprintf("%s/%s/%s", config.AppCnf.UploadFileSettings.Path, sid, otherParts)
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

func HandleConvertWhiteboardFile(c *fiber.Ctx) error {
	req := new(models.ManageFile)
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

	if req.FilePath == "" {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "file path require",
		})
	}

	m := models.NewManageFileModel(req)
	res, err := m.ConvertWhiteboardFile()
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"status":      true,
		"msg":         "success",
		"file_id":     res.FileId,
		"file_name":   res.FileName,
		"file_path":   res.FilePath,
		"total_pages": res.TotalPages,
	})
}
