package controllers

import (
	"fmt"
	"github.com/gabriel-vasile/mimetype"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
	"net/url"
	"strconv"
	"strings"
)

// HandleFileUpload method can only be use if you are using resumable.js as your frontend.
// Library link: https://github.com/23/resumable.js
func HandleFileUpload(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")

	// this will be used to verify regarding file origin only
	req := new(models.ResumableUploadReq)
	err := c.QueryParser(req)
	if err != nil {
		_ = c.SendStatus(fiber.StatusBadRequest)
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	if req.RoomSid == "" || req.RoomId == "" || req.UserId == "" {
		return commonFileErrorResponse(c, "missing required fields")
	}
	if roomId != req.RoomId {
		return commonFileErrorResponse(c, "token roomId & requested roomId didn't matched")
	}
	if requestedUserId != req.UserId {
		return commonFileErrorResponse(c, "token roomId & requested roomId didn't matched")
	}

	m := models.NewFileModel(nil, nil, nil)
	res, err := m.ResumableFileUpload(c)
	if err != nil {
		return commonFileErrorResponse(c, err.Error())
	}

	if res.FilePath == "part_uploaded" {
		_ = c.SendStatus(fiber.StatusOK)
		return c.SendString(res.FilePath)
	} else {
		res.Status = true
		res.Msg = "file uploaded successfully"
		return c.JSON(res)
	}
}

func HandleUploadedFileMerge(c *fiber.Ctx) error {
	req := new(models.ResumableUploadedFileMergeReq)
	err := c.BodyParser(req)
	if err != nil {
		return commonFileErrorResponse(c, err.Error())
	}

	if req.RoomSid == "" || req.RoomId == "" {
		return commonFileErrorResponse(c, "missing required fields")
	}

	m := models.NewFileModel(nil, nil, nil)
	res, err := m.UploadedFileMerge(req)
	if err != nil {
		return commonFileErrorResponse(c, err.Error())
	}

	return c.JSON(res)
}

func HandleUploadBase64EncodedData(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")

	req := new(plugnmeet.UploadBase64EncodedDataReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	req.RoomId = roomId.(string)
	m := models.NewFileModel(nil, nil, nil)
	res, err := m.UploadBase64EncodedData(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendProtobufResponse(c, res)
}

func HandleDownloadUploadedFile(c *fiber.Ctx) error {
	sid := c.Params("sid")
	otherParts := c.Params("*")
	otherParts, _ = url.QueryUnescape(otherParts)

	file := fmt.Sprintf("%s/%s/%s", config.GetConfig().UploadFileSettings.Path, sid, otherParts)
	mtype, err := mimetype.DetectFile(file)
	if err != nil {
		ms := strings.SplitN(err.Error(), "/", -1)
		return c.Status(fiber.StatusNotFound).SendString(ms[len(ms)-1])
	}

	ff := strings.SplitN(file, "/", -1)
	c.Set("Content-Disposition", "attachment; filename="+strconv.Quote(ff[len(ff)-1]))
	c.Set("Content-Type", mtype.String())

	return c.SendFile(file)
}

func HandleConvertWhiteboardFile(c *fiber.Ctx) error {
	req := new(models.ConvertWhiteboardFileReq)
	err := c.BodyParser(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	if req.RoomSid == "" || req.RoomId == "" {
		_ = c.SendStatus(fiber.StatusBadRequest)
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "missing required fields",
		})
	}

	if req.FilePath == "" {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "file path require",
		})
	}

	m := models.NewFileModel(nil, nil, nil)
	res, err := m.ConvertAndBroadcastWhiteboardFile(req.RoomId, req.RoomSid, req.FilePath)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(res)
}

func HandleGetClientFiles(c *fiber.Ctx) error {
	var css, js []string

	if config.GetConfig().Client.Debug {
		var err error
		css, err = utils.GetFilesFromDir(config.GetConfig().Client.Path+"/assets/css", ".css", "des")
		if err != nil {
			log.Errorln(err)
		}

		js, err = utils.GetFilesFromDir(config.GetConfig().Client.Path+"/assets/js", ".js", "asc")
		if err != nil {
			log.Errorln(err)
		}
	} else {
		css = config.GetConfig().ClientFiles["css"]
		js = config.GetConfig().ClientFiles["js"]
	}

	return c.JSON(fiber.Map{
		"status": true,
		"msg":    "success",
		"css":    css,
		"js":     js,
	})
}

func commonFileErrorResponse(c *fiber.Ctx, msg string) error {
	return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
		"status": false,
		"msg":    msg,
	})
}
