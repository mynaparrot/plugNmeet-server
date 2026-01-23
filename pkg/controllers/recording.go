package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	dbservice "github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
)

// RecordingController holds dependencies for recording-related handlers.
type RecordingController struct {
	ds             *dbservice.DatabaseService
	recordingModel *models.RecordingModel
	logger         *logrus.Entry
}

// NewRecordingController creates a new RecordingController.
func NewRecordingController(ds *dbservice.DatabaseService, recordingModel *models.RecordingModel, logger *logrus.Logger) *RecordingController {
	return &RecordingController{
		ds:             ds,
		recordingModel: recordingModel,
		logger:         logger.WithField("controller", "recording"),
	}
}

// HandleFetchRecordings handles fetching recordings.
func (rc *RecordingController) HandleFetchRecordings(c *fiber.Ctx) error {
	req := new(plugnmeet.FetchRecordingsReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	result, err := rc.recordingModel.FetchRecordings(req)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}
	if result.GetTotalRecordings() == 0 {
		return utils.SendCommonProtoJsonResponse(c, false, "no recordings found")
	}

	r := &plugnmeet.FetchRecordingsRes{
		Status: true,
		Msg:    "success",
		Result: result,
	}
	return utils.SendProtoJsonResponse(c, r)
}

// HandleRecordingInfo handles fetching information for a single recording.
func (rc *RecordingController) HandleRecordingInfo(c *fiber.Ctx) error {
	req := new(plugnmeet.RecordingInfoReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	result, err := rc.recordingModel.RecordingInfo(req)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	return utils.SendProtoJsonResponse(c, result)
}

// HandleUpdateRecordingMetadata handles update metadata information for a single recording.
func (rc *RecordingController) HandleUpdateRecordingMetadata(c *fiber.Ctx) error {
	req := new(plugnmeet.UpdateRecordingMetadataReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	err := rc.recordingModel.UpdateRecordingMetadata(req)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	return utils.SendCommonProtoJsonResponse(c, true, "success")
}

// HandleDeleteRecording handles deleting a recording.
func (rc *RecordingController) HandleDeleteRecording(c *fiber.Ctx) error {
	req := new(plugnmeet.DeleteRecordingReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	err := rc.recordingModel.DeleteRecording(req)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	return utils.SendCommonProtoJsonResponse(c, true, "success")
}

// HandleGetDownloadToken handles generating a download token for a recording.
func (rc *RecordingController) HandleGetDownloadToken(c *fiber.Ctx) error {
	req := new(plugnmeet.GetDownloadTokenReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	token, err := rc.recordingModel.GetDownloadToken(req)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	r := &plugnmeet.GetDownloadTokenRes{
		Status: true,
		Msg:    "success",
		Token:  &token,
	}
	return utils.SendProtoJsonResponse(c, r)
}

// HandleDownloadRecording handles downloading a recording file.
func (rc *RecordingController) HandleDownloadRecording(c *fiber.Ctx) error {
	token := c.Params("token")

	if len(token) == 0 {
		return c.Status(fiber.StatusUnauthorized).SendString("token require or invalid url")
	}

	file, status, err := rc.recordingModel.VerifyRecordingToken(token)
	if err != nil {
		return c.Status(status).SendString(err.Error())
	}

	c.Attachment(file)
	return c.SendFile(file, false)
}

// HandleRecorderTasks handles start/stop recording & RTMP requests.
func (rc *RecordingController) HandleRecorderTasks(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")

	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can start recording")
	}

	if roomId == "" {
		return utils.SendCommonProtobufResponse(c, false, "no roomId in token")
	}

	req := new(plugnmeet.RecordingReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	// now need to check if meeting is running or not
	isRunning := 1
	room, err := rc.ds.GetRoomInfoBySid(req.Sid, &isRunning)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	if room == nil || room.ID == 0 {
		return utils.SendCommonProtobufResponse(c, false, "notifications.room-not-active")
	}

	if room.RoomId != roomId {
		return utils.SendCommonProtobufResponse(c, false, "roomId in token mismatched")
	}

	switch req.Task {
	case plugnmeet.RecordingTasks_START_RECORDING:
		if room.IsRecording == 1 {
			return utils.SendCommonProtobufResponse(c, false, "notifications.recording-already-running")
		}
	case plugnmeet.RecordingTasks_STOP_RECORDING:
		if room.IsRecording == 0 {
			return utils.SendCommonProtobufResponse(c, false, "notifications.recording-not-running")
		}
	case plugnmeet.RecordingTasks_START_RTMP:
		if req.RtmpUrl == nil {
			return utils.SendCommonProtobufResponse(c, false, "rtmpUrl required")
		}
		if room.IsActiveRtmp == 1 {
			return utils.SendCommonProtobufResponse(c, false, "notifications.rtmp-already-running")
		}
	case plugnmeet.RecordingTasks_STOP_RTMP:
		if room.IsActiveRtmp == 0 {
			return utils.SendCommonProtobufResponse(c, false, "notifications.rtmp-not-running")
		}
	}

	req.RoomId = room.RoomId
	req.RoomTableId = int64(room.ID)

	err = rc.recordingModel.DispatchRecorderTask(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

// HandleRecorderEvents handles events coming from the recorder.
func (rc *RecordingController) HandleRecorderEvents(c *fiber.Ctx) error {
	req := new(plugnmeet.RecorderToPlugNmeet)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		rc.logger.WithError(err).Errorln("unmarshalling failed")
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
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
