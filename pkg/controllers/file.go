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

// FileController holds dependencies for file-related handlers.
type FileController struct {
	AppConfig *config.AppConfig
	FileModel *models.FileModel
}

// NewFileController creates a new FileController.
func NewFileController(config *config.AppConfig, fm *models.FileModel) *FileController {
	return &FileController{
		AppConfig: config,
		FileModel: fm,
	}
}

// HandleFileUpload method can only be use if you are using resumable.js as your frontend.
// Library link: https://github.com/23/resumable.js
func (fc *FileController) HandleFileUpload(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")

	// this will be used to verify regarding file origin only
	req := new(models.ResumableUploadReq)
	err := c.QueryParser(req)
	if err != nil {
		return commonFileErrorResponse(c, err.Error(), fiber.StatusBadRequest)
	}

	if req.RoomSid == "" || req.RoomId == "" || req.UserId == "" {
		return commonFileErrorResponse(c, "missing required fields", fiber.StatusBadRequest)
	}
	if roomId != req.RoomId {
		return commonFileErrorResponse(c, "token roomId & requested roomId didn't matched", fiber.StatusBadRequest)
	}
	if requestedUserId != req.UserId {
		return commonFileErrorResponse(c, "token roomId & requested roomId didn't matched", fiber.StatusBadRequest)
	}

	res, fErr := fc.FileModel.ResumableFileUpload(c)
	if fErr != nil {
		return commonFileErrorResponse(c, fErr.Message, fErr.Code)
	}

	if res.FilePath == "part_uploaded" {
		_ = c.SendStatus(fiber.StatusOK)
		return c.SendString(res.FilePath)
	} else {
		return c.SendString(res.Msg)
	}
}

// HandleUploadedFileMerge handles merging chunks of a resumable upload.
func (fc *FileController) HandleUploadedFileMerge(c *fiber.Ctx) error {
	req := new(models.ResumableUploadedFileMergeReq)
	err := c.BodyParser(req)
	if err != nil {
		return commonFileErrorResponse(c, err.Error(), fiber.StatusBadRequest)
	}

	if req.RoomSid == "" || req.RoomId == "" {
		return commonFileErrorResponse(c, "missing required fields", fiber.StatusBadRequest)
	}

	res, err := fc.FileModel.UploadedFileMerge(req)
	if err != nil {
		return commonFileErrorResponse(c, err.Error(), fiber.StatusBadRequest)
	}

	return c.JSON(res)
}

// HandleUploadBase64EncodedData handles uploading base64 encoded data.
func (fc *FileController) HandleUploadBase64EncodedData(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")

	req := new(plugnmeet.UploadBase64EncodedDataReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	req.RoomId = roomId.(string)
	res, err := fc.FileModel.UploadBase64EncodedData(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendProtobufResponse(c, res)
}

// HandleDownloadUploadedFile handles downloading an uploaded file.
func (fc *FileController) HandleDownloadUploadedFile(c *fiber.Ctx) error {
	sid := c.Params("sid")
	otherParts := c.Params("*")
	otherParts, _ = url.QueryUnescape(otherParts)

	file := fmt.Sprintf("%s/%s/%s", fc.AppConfig.UploadFileSettings.Path, sid, otherParts)
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

// HandleConvertWhiteboardFile handles converting a file for the whiteboard.
func (fc *FileController) HandleConvertWhiteboardFile(c *fiber.Ctx) error {
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

	res, err := fc.FileModel.ConvertAndBroadcastWhiteboardFile(req.RoomId, req.RoomSid, req.FilePath)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(res)
}

// HandleGetClientFiles gets the client CSS and JS files.
func (fc *FileController) HandleGetClientFiles(c *fiber.Ctx) error {
	var css, js []string

	if fc.AppConfig.Client.Debug {
		var err error
		css, err = utils.GetFilesFromDir(fc.AppConfig.Client.Path+"/assets/css", ".css", "des")
		if err != nil {
			log.Errorln(err)
		}

		js, err = utils.GetFilesFromDir(fc.AppConfig.Client.Path+"/assets/js", ".js", "asc")
		if err != nil {
			log.Errorln(err)
		}
	} else {
		css = fc.AppConfig.ClientFiles["css"]
		js = fc.AppConfig.ClientFiles["js"]
	}

	return c.JSON(fiber.Map{
		"status": true,
		"msg":    "success",
		"css":    css,
		"js":     js,
	})
}

func commonFileErrorResponse(c *fiber.Ctx, msg string, status int) error {
	if status > 0 {
		_ = c.SendStatus(status)
	}
	return c.JSON(fiber.Map{
		"status": false,
		"msg":    msg,
	})
}
