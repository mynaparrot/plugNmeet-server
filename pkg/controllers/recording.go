package controllers

import (
	"errors"
	"net/url"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	dbservice "github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
	"google.golang.org/protobuf/proto"
)

// RecordingController holds dependencies for recording-related handlers.
type RecordingController struct {
	ds             *dbservice.DatabaseService
	recordingModel *models.RecordingModel
	logger         *logrus.Entry
}

type RecordingControllerArgs struct {
	fx.In
	Ds             *dbservice.DatabaseService
	RecordingModel *models.RecordingModel
	Logger         *logrus.Logger
}

// NewRecordingController creates a new RecordingController.
func NewRecordingController(args RecordingControllerArgs) *RecordingController {
	return &RecordingController{
		ds:             args.Ds,
		recordingModel: args.RecordingModel,
		logger:         args.Logger.WithField("controller", "recording"),
	}
}

// HandleFetchRecordings handles fetching recordings.
func (rc *RecordingController) HandleFetchRecordings(c fiber.Ctx) error {
	req := new(plugnmeet.FetchRecordingsReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, config.MsgAuthInvalidRequest, plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	result, err := rc.recordingModel.FetchRecordings(req)
	if err != nil {
		if errors.Is(err, config.NotFoundErr) {
			return utils.SendCommonProtoJsonResponse(c, false, config.MsgAuthRecordingNotFound, plugnmeet.StatusCode_NOT_FOUND)
		}
		return utils.SendCommonProtoJsonResponse(c, false, config.MsgAuthRecordingsFetchFailed, plugnmeet.StatusCode_INTERNAL_SERVER_ERROR)
	}

	r := &plugnmeet.FetchRecordingsRes{
		Status:     true,
		Msg:        config.MsgIntegratorSuccess,
		StatusCode: plugnmeet.StatusCode_SUCCESS,
		Result:     result,
	}
	return utils.SendProtoJsonResponse(c, r)
}

// HandleRecordingInfo handles fetching information for a single recording.
func (rc *RecordingController) HandleRecordingInfo(c fiber.Ctx) error {
	req := new(plugnmeet.RecordingInfoReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, config.MsgAuthInvalidRequest, plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	result, err := rc.recordingModel.RecordingInfo(req)
	if err != nil {
		if errors.Is(err, config.NotFoundErr) {
			return utils.SendCommonProtoJsonResponse(c, false, config.MsgAuthRecordingNotFound, plugnmeet.StatusCode_NOT_FOUND)
		}
		return utils.SendCommonProtoJsonResponse(c, false, config.MsgAuthRecordingsFetchFailed, plugnmeet.StatusCode_INTERNAL_SERVER_ERROR)
	}

	return utils.SendProtoJsonResponse(c, result)
}

// HandleUpdateRecordingMetadata handles update metadata information for a single recording.
func (rc *RecordingController) HandleUpdateRecordingMetadata(c fiber.Ctx) error {
	req := new(plugnmeet.UpdateRecordingMetadataReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, config.MsgAuthInvalidRequest, plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	err := rc.recordingModel.UpdateRecordingMetadata(req)
	if err != nil {
		if errors.Is(err, config.NotFoundErr) {
			return utils.SendCommonProtoJsonResponse(c, false, config.MsgAuthRecordingNotFound, plugnmeet.StatusCode_NOT_FOUND)
		}
		return utils.SendCommonProtoJsonResponse(c, false, config.MsgAuthRecordingUpdateFailed, plugnmeet.StatusCode_INTERNAL_SERVER_ERROR)
	}

	return utils.SendCommonProtoJsonResponse(c, true, config.MsgIntegratorSuccess, plugnmeet.StatusCode_SUCCESS)
}

// HandleMergeRecordings handles merging multiple recordings into a single new one.
func (rc *RecordingController) HandleMergeRecordings(c fiber.Ctx) error {
	req := new(plugnmeet.MergeRecordingsReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, config.MsgAuthInvalidRequest, plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	statusCode, err := rc.recordingModel.MergeRecordings(c.RequestCtx(), req)
	if err != nil {
		if errors.Is(err, config.NotFoundErr) {
			return utils.SendCommonProtoJsonResponse(c, false, config.MsgAuthRecordingNotFound, plugnmeet.StatusCode_NOT_FOUND)
		}
		return utils.SendCommonProtoJsonResponse(c, false, config.MsgAuthRecordingMergeFailed, statusCode)
	}

	return utils.SendCommonProtoJsonResponse(c, true, config.MsgIntegratorSuccess, statusCode)
}

// HandleDeleteRecording handles deleting a recording.
func (rc *RecordingController) HandleDeleteRecording(c fiber.Ctx) error {
	req := new(plugnmeet.DeleteRecordingReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, config.MsgAuthInvalidRequest, plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	err := rc.recordingModel.DeleteRecording(req)
	if err != nil {
		if errors.Is(err, config.NotFoundErr) {
			return utils.SendCommonProtoJsonResponse(c, false, config.MsgAuthRecordingNotFound, plugnmeet.StatusCode_NOT_FOUND)
		}
		return utils.SendCommonProtoJsonResponse(c, false, config.MsgAuthRecordingDeleteFailed, plugnmeet.StatusCode_INTERNAL_SERVER_ERROR)
	}

	return utils.SendCommonProtoJsonResponse(c, true, config.MsgIntegratorSuccess, plugnmeet.StatusCode_SUCCESS)
}

// HandleGetDownloadToken handles generating a download token for a recording.
func (rc *RecordingController) HandleGetDownloadToken(c fiber.Ctx) error {
	req := new(plugnmeet.GetDownloadTokenReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, config.MsgAuthInvalidRequest, plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	token, err := rc.recordingModel.GetDownloadToken(req)
	if err != nil {
		if errors.Is(err, config.NotFoundErr) {
			return utils.SendCommonProtoJsonResponse(c, false, config.MsgAuthRecordingNotFound, plugnmeet.StatusCode_NOT_FOUND)
		}
		return utils.SendCommonProtoJsonResponse(c, false, config.MsgAuthDownloadTokenFailed, plugnmeet.StatusCode_INTERNAL_SERVER_ERROR)
	}

	r := &plugnmeet.GetDownloadTokenRes{
		Status:     true,
		Msg:        config.MsgIntegratorSuccess,
		StatusCode: plugnmeet.StatusCode_SUCCESS,
		Token:      &token,
	}
	return utils.SendProtoJsonResponse(c, r)
}

// HandleDownloadRecording handles downloading a recording file.
func (rc *RecordingController) HandleDownloadRecording(c fiber.Ctx) error {
	token := c.Params("token")

	if token == "" {
		return c.Status(fiber.StatusUnauthorized).SendString(config.MsgFileDownloadTokenInvalid)
	}

	res, status, err := rc.recordingModel.VerifyRecordingToken(token)
	if err != nil {
		return downloadTokenErrorResponse(c, res, status, err, rc.logger)
	}

	return serveDownloadHookResult(c, res, rc.logger)
}

// isValidRtmpUrl allows only absolute rtmp/rtmps urls.
func isValidRtmpUrl(rawUrl string) bool {
	parsedURL, err := url.ParseRequestURI(rawUrl)
	if err != nil || parsedURL.Host == "" {
		return false
	}
	scheme := strings.ToLower(parsedURL.Scheme)
	return scheme == "rtmp" || scheme == "rtmps"
}

// HandleRecorderTasks handles start/stop recording & RTMP requests.
func (rc *RecordingController) HandleRecorderTasks(c fiber.Ctx) error {
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	roomId := fiber.Locals[string](c, "roomId")
	roomSid := fiber.Locals[string](c, "roomSid")

	if !isAdmin {
		return utils.SendCommonProtobufResponse(c, false, config.ErrUserOnlyAdmin.Error())
	}

	if roomId == "" {
		return utils.SendCommonProtobufResponse(c, false, config.ErrRoomIdRequired.Error())
	}

	req := new(plugnmeet.RecordingReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		// expected malformed caller input: no server-side log
		return utils.SendCommonProtobufResponse(c, false, config.ErrRecordingInvalidRequest.Error())
	}

	if req.Sid != roomSid {
		return utils.SendCommonProtobufResponse(c, false, config.ErrUserRoomSidMismatch.Error())
	}

	// now need to check if meeting is running or not
	room, err := rc.ds.GetRoomInfoBySid(roomSid, new(1))
	if err != nil {
		rc.logger.WithError(err).WithFields(logrus.Fields{
			"roomId":  roomId,
			"roomSid": roomSid,
		}).Errorln("failed to fetch room info by sid")
		return utils.SendCommonProtobufResponse(c, false, config.ErrRecordingOperationFailed.Error())
	}

	if room == nil || room.ID == 0 {
		return utils.SendCommonProtobufResponse(c, false, config.ErrAuthRoomNotActive.Error())
	}

	if room.RoomId != roomId {
		return utils.SendCommonProtobufResponse(c, false, config.ErrUserRoomIdMismatch.Error())
	}

	switch req.Task {
	case plugnmeet.RecordingTasks_START_RECORDING:
		if room.IsRecording == 1 {
			return utils.SendCommonProtobufResponse(c, false, config.ErrRecordingAlreadyRunning.Error())
		}
	case plugnmeet.RecordingTasks_STOP_RECORDING:
		if room.IsRecording == 0 {
			return utils.SendCommonProtobufResponse(c, false, config.ErrRecordingNotRunning.Error())
		}
	case plugnmeet.RecordingTasks_START_RTMP:
		if req.RtmpUrl == nil || *req.RtmpUrl == "" {
			return utils.SendCommonProtobufResponse(c, false, config.ErrRecordingRtmpUrlRequired.Error())
		}
		if !isValidRtmpUrl(*req.RtmpUrl) {
			return utils.SendCommonProtobufResponse(c, false, config.ErrRecordingInvalidRtmpUrl.Error())
		}
		if room.IsActiveRtmp == 1 {
			return utils.SendCommonProtobufResponse(c, false, config.ErrRecordingRtmpAlreadyRunning.Error())
		}
	case plugnmeet.RecordingTasks_STOP_RTMP:
		if room.IsActiveRtmp == 0 {
			return utils.SendCommonProtobufResponse(c, false, config.ErrRecordingRtmpNotRunning.Error())
		}
	}

	req.RoomId = room.RoomId
	req.RoomTableId = int64(room.ID)

	err = rc.recordingModel.DispatchRecorderTask(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, config.MsgSuccess)
}

// HandleRecorderEvents handles events coming from the recorder.
func (rc *RecordingController) HandleRecorderEvents(c fiber.Ctx) error {
	req := new(plugnmeet.RecorderToPlugNmeet)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		rc.logger.WithError(err).Errorln("unmarshalling failed")
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    config.MsgAuthInvalidRequest,
		})
	}

	if req.From == "recorder" {
		roomInfo, err := rc.ds.GetRoomInfoByTableId(uint64(req.RoomTableId))
		if err != nil {
			rc.logger.WithError(err).Errorln("error getting room info")
			return c.SendStatus(fiber.StatusInternalServerError)
		}
		if roomInfo == nil {
			return c.SendStatus(fiber.StatusNotFound)
		}

		req.RoomId = roomInfo.RoomId
		req.RoomSid = roomInfo.Sid
		rc.recordingModel.ProcessRecorderEvent(req, roomInfo)
	}

	return c.SendStatus(fiber.StatusOK)
}
