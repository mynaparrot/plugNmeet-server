package controllers

import (
	"context"
	"errors"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-protocol/hooks"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
	"google.golang.org/protobuf/proto"
)

// FileController holds dependencies for file-related handlers.
type FileController struct {
	AppConfig *config.AppConfig
	FileModel *models.FileModel
	RoomModel *models.RoomModel
	logger    *logrus.Entry
}

type FileControllerArgs struct {
	fx.In
	AppConfig *config.AppConfig
	FileModel *models.FileModel
	RoomModel *models.RoomModel
	Logger    *logrus.Logger
}

// NewFileController creates a new FileController.
func NewFileController(args FileControllerArgs) *FileController {
	return &FileController{
		AppConfig: args.AppConfig,
		FileModel: args.FileModel,
		RoomModel: args.RoomModel,
		logger:    args.Logger.WithField("controller", "file"),
	}
}

// HandleFileUpload method can only be use if you are using resumable.js as your frontend.
// Library link: https://github.com/23/resumable.js
func (fc *FileController) HandleFileUpload(c fiber.Ctx) error {
	// this will be used to verify regarding file origin only
	req := new(models.ResumableUploadReq)
	if err := c.Bind().Query(req); err != nil {
		return commonFileErrorResponse(c, err.Error(), fiber.StatusBadRequest, plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	req.RoomId = fiber.Locals[string](c, "roomId")
	req.RoomSid = fiber.Locals[string](c, "roomSid")
	req.UserId = fiber.Locals[string](c, "requestedUserId")

	if req.RoomSid == "" || req.RoomId == "" {
		return commonFileErrorResponse(c, "missing required fields", fiber.StatusBadRequest, plugnmeet.StatusCode_INTERNAL_SERVER_ERROR)
	}

	res, fErr := fc.FileModel.ResumableFileUpload(c, req)
	if fErr != nil {
		return commonFileErrorResponse(c, fErr.Message, fErr.Code, plugnmeet.StatusCode_INTERNAL_SERVER_ERROR)
	}

	if res.FilePath == "part_uploaded" {
		_ = c.SendStatus(fiber.StatusOK)
		return c.SendString(res.FilePath)
	}

	return c.SendString(res.Msg)
}

// HandleUploadedFileMerge handles merging chunks of a resumable upload.
func (fc *FileController) HandleUploadedFileMerge(c fiber.Ctx) error {
	req := new(plugnmeet.UploadedFileMergeReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	req.RoomId = fiber.Locals[string](c, "roomId")
	req.RoomSid = fiber.Locals[string](c, "roomSid")
	res, err := fc.FileModel.UploadedFileMerge(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendProtobufResponse(c, res)
}

// HandleUploadBase64EncodedData handles uploading base64 encoded data.
func (fc *FileController) HandleUploadBase64EncodedData(c fiber.Ctx) error {
	roomId := fiber.Locals[string](c, "roomId")
	roomSid := fiber.Locals[string](c, "roomSid")

	req := new(plugnmeet.UploadBase64EncodedDataReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	req.RoomId = roomId
	res, err := fc.FileModel.UploadBase64EncodedData(req, roomSid)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendProtobufResponse(c, res)
}

// HandleUploadWhiteboardFile will upload file from Auth API.
// Uploading from client we use resumable.js library not this endpoint.
func (fc *FileController) HandleUploadWhiteboardFile(c fiber.Ctx) error {
	roomId := c.Get(config.HeaderRoomId)
	if roomId == "" {
		return commonFileErrorResponse(c, "missing required header Room-Id", fiber.StatusBadRequest, plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	res, rf, _ := fc.RoomModel.IsRoomActive(&plugnmeet.IsRoomActiveReq{RoomId: roomId})
	if !res.GetIsActive() {
		return commonFileErrorResponse(c, res.GetMsg(), fiber.StatusBadRequest, res.GetStatusCode())
	}

	statusCode, err := fc.FileModel.UploadWhiteboardFileFromAuthApi(c, rf)
	if err != nil {
		return commonFileErrorResponse(c, err.Error(), err.Code, statusCode)
	}

	return utils.SendCommonProtoJsonResponse(c, true, "file uploaded successfully", plugnmeet.StatusCode_SUCCESS)
}

// HandleDownloadUploadedFile handles downloading an uploaded file.
func (fc *FileController) HandleDownloadUploadedFile(c fiber.Ctx) error {
	unescapedPath, err := url.QueryUnescape(c.Params("*"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("invalid file path")
	}

	relativePath := path.Clean(unescapedPath)
	pathSegments := strings.Split(relativePath, "/")

	// must have at least 2 segments e.g. roomSid/other parts
	if len(pathSegments) < 2 {
		return c.Status(fiber.StatusBadRequest).SendString("invalid url")
	}

	for _, segment := range pathSegments {
		// prevent to download from temp dir by checking path segments
		if segment == config.UploadFileTempDir {
			return c.Status(fiber.StatusForbidden).SendString("access to temporary directory is forbidden")
		}
	}

	if fc.AppConfig.Hooks != nil {
		req := hooks.DownloadHookData{
			InputPath:    relativePath,
			HookFileType: hooks.HookFileTypeRoomFile,
		}
		res, err := fc.AppConfig.Hooks.RunDownloadHook(c.RequestCtx(), &req, nil, 0, fc.logger)
		if err != nil {
			fc.logger.WithError(err).Error("download hook pipeline failed")
			return c.Status(fiber.StatusInternalServerError).SendString("download hook pipeline failed")
		}
		if res != nil && res.RedirectUrl != "" {
			return c.Redirect().Status(fiber.StatusTemporaryRedirect).To(res.RedirectUrl)
		}
	}

	absFile, mType, err := helpers.ValidateAndGetAbsFilePath(fc.AppConfig.UploadFileSettings.Path, relativePath)
	if err != nil {
		fc.logger.WithError(err).Warn("file path validation failed")
		if errors.Is(err, config.ErrFileNotFound) {
			return c.Status(fiber.StatusNotFound).SendString(err.Error())
		}
		return c.Status(fiber.StatusBadRequest).SendString("invalid file path")
	}

	c.Set(fiber.HeaderContentType, mType.String())
	return c.SendFile(absFile, fiber.SendFile{
		Download: true,
	})
}

// HandleConvertWhiteboardFile handles converting a file for the whiteboard.
func (fc *FileController) HandleConvertWhiteboardFile(c fiber.Ctx) error {
	req := new(models.ConvertWhiteboardFileReq)
	if err := c.Bind().Body(req); err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}
	req.RoomId = fiber.Locals[string](c, "roomId")
	req.RoomSid = fiber.Locals[string](c, "roomSid")
	requestedUserId := new(fiber.Locals[string](c, "requestedUserId"))

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
	log := fc.logger.WithField("method", "HandleConvertWhiteboardFile")

	// We'll give 50 seconds to complete the task
	ctx, cancel := context.WithTimeout(c.RequestCtx(), 50*time.Second)
	defer cancel()

	res, err := fc.FileModel.ConvertAndBroadcastWhiteboardFile(ctx, req.RoomId, req.RoomSid, req.FilePath, requestedUserId, nil, log)
	if err != nil {
		if errors.Is(err, config.ErrConversionTimeout) {
			// process will continue in background
			return c.JSON(fiber.Map{
				"status": true,
				"msg":    "File conversion started. It will be available soon.",
			})
		}
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(res)
}

func (fc *FileController) HandleWhiteboardPdfExportUpload(c fiber.Ctx) error {
	req := new(models.WhiteboardPdfExportUploadReq)
	if err := c.Bind().Form(req); err != nil {
		return commonFileErrorResponse(c, err.Error(), fiber.StatusBadRequest, plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	req.RoomId = fiber.Locals[string](c, "roomId")
	req.RoomSid = fiber.Locals[string](c, "roomSid")
	req.UserId = fiber.Locals[string](c, "requestedUserId")

	acState, _, _ := fc.RoomModel.IsRoomActive(&plugnmeet.IsRoomActiveReq{RoomId: req.RoomId})
	if !acState.GetIsActive() {
		return commonFileErrorResponse(c, acState.GetMsg(), fiber.StatusBadRequest, acState.GetStatusCode())
	}

	if fErr := fc.FileModel.WhiteboardPdfExportUpload(c, req); fErr != nil {
		return commonFileErrorResponse(c, fErr.Message, fErr.Code, plugnmeet.StatusCode_INTERNAL_SERVER_ERROR)
	}

	return c.SendString("file uploaded successfully")
}

func (fc *FileController) HandleWhiteboardPdfExportFileMerge(c fiber.Ctx) error {
	req := new(plugnmeet.UploadedFileMergeReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	req.RoomId = fiber.Locals[string](c, "roomId")
	req.RoomSid = fiber.Locals[string](c, "roomSid")
	requestedUserId := fiber.Locals[string](c, "requestedUserId")

	acState, _, _ := fc.RoomModel.IsRoomActive(&plugnmeet.IsRoomActiveReq{RoomId: req.RoomId})
	if !acState.GetIsActive() {
		return commonFileErrorResponse(c, acState.GetMsg(), fiber.StatusBadRequest, acState.GetStatusCode())
	}

	res, err := fc.FileModel.BuildWhiteboardPdfExportFile(req, requestedUserId)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}
	return utils.SendProtobufResponse(c, res)
}

func (fc *FileController) HandleGetRoomFilesByType(c fiber.Ctx) error {
	req := new(plugnmeet.GetRoomUploadedFilesReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	res, err := fc.FileModel.GetRoomFilesByType(req.RoomId, req.FileType)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendProtobufResponse(c, res)
}

// HandleGetClientFiles gets the client CSS and JS files.
// this also depends on config's readClientFiles method
func (fc *FileController) HandleGetClientFiles(c fiber.Ctx) error {
	var cssFiles, jsFiles []string

	if fc.AppConfig.Client.Debug {
		var err error
		cssFiles, err = utils.GetFilesFromDir(path.Join(fc.AppConfig.Client.Path, "assets", "css"), ".css", "des")
		if err != nil {
			fc.logger.WithError(err).Errorln("error getting css files")
		}

		jsFiles, err = utils.GetFilesFromDir(path.Join(fc.AppConfig.Client.Path, "assets", "js"), ".js", "asc")
		if err != nil {
			fc.logger.WithError(err).Errorln("error getting js files")
		}
	} else {
		cssFiles = fc.AppConfig.ClientFiles["css"]
		jsFiles = fc.AppConfig.ClientFiles["js"]
	}

	return utils.SendProtoJsonResponse(c, &plugnmeet.GetClientFilesRes{
		Status:           true,
		Msg:              "success",
		Css:              cssFiles,
		Js:               jsFiles,
		JsFiles:          jsFiles,
		CssFiles:         cssFiles,
		StaticAssetsPath: fc.AppConfig.Client.AssetHost,
	})
}

func commonFileErrorResponse(c fiber.Ctx, msg string, status int, statusCode plugnmeet.StatusCode) error {
	if status > 0 {
		_ = c.SendStatus(status)
	}
	return c.JSON(fiber.Map{
		"status":      false,
		"msg":         msg,
		"status_code": statusCode.String(),
	})
}
