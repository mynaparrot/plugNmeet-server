package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
)

// RecorderController holds dependencies for recorder-related handlers.
type RecorderController struct {
	AppConfig      *config.AppConfig
	RecorderModel  *models.RecorderModel
	RecordingModel *models.RecordingModel
	RoomModel      *models.RoomModel
	ds             *dbservice.DatabaseService
	logger         *logrus.Entry
}

// NewRecorderController creates a new RecorderController.
func NewRecorderController(config *config.AppConfig, ds *dbservice.DatabaseService, recorderModel *models.RecorderModel, recordingModel *models.RecordingModel, roomModel *models.RoomModel, logger *logrus.Logger) *RecorderController {
	return &RecorderController{
		AppConfig:      config,
		RecorderModel:  recorderModel,
		RecordingModel: recordingModel,
		RoomModel:      roomModel,
		ds:             ds,
		logger:         logger.WithField("controller", "recorder"),
	}
}

// HandleRecording handles start/stop recording requests.
func (rc *RecorderController) HandleRecording(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")

	if isAdmin != true {
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

	err = req.Validate()
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

	if room.IsRecording == 1 && req.Task == plugnmeet.RecordingTasks_START_RECORDING {
		return utils.SendCommonProtobufResponse(c, false, "notifications.recording-already-running")
	} else if room.IsRecording == 0 && req.Task == plugnmeet.RecordingTasks_STOP_RECORDING {
		return utils.SendCommonProtobufResponse(c, false, "notifications.recording-not-running")
	}

	if room.IsActiveRtmp == 1 && req.Task == plugnmeet.RecordingTasks_START_RTMP {
		return utils.SendCommonProtobufResponse(c, false, "notifications.rtmp-already-running")
	} else if room.IsActiveRtmp == 0 && req.Task == plugnmeet.RecordingTasks_STOP_RTMP {
		return utils.SendCommonProtobufResponse(c, false, "notifications.rtmp-not-running")
	}

	req.RoomId = room.RoomId
	req.RoomTableId = int64(room.ID)

	err = rc.RecorderModel.SendMsgToRecorder(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

// HandleRTMP handles start/stop RTMP requests.
func (rc *RecorderController) HandleRTMP(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")

	if isAdmin != true {
		return utils.SendCommonProtobufResponse(c, false, "only admin can start recording")
	}

	if roomId == "" {
		return utils.SendCommonProtobufResponse(c, false, "no roomId in token")
	}

	// we can use same as RecordingReq
	req := new(plugnmeet.RecordingReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	err = req.Validate()
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	if req.Task == plugnmeet.RecordingTasks_START_RTMP {
		if req.RtmpUrl == nil {
			return utils.SendCommonProtobufResponse(c, false, "rtmpUrl required")
		}
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

	if room.IsActiveRtmp == 1 && req.Task == plugnmeet.RecordingTasks_START_RTMP {
		return utils.SendCommonProtobufResponse(c, false, "RTMP broadcasting already running")
	} else if room.IsActiveRtmp == 0 && req.Task == plugnmeet.RecordingTasks_STOP_RTMP {
		return utils.SendCommonProtobufResponse(c, false, "RTMP broadcasting not running")
	}

	req.RoomId = room.RoomId
	req.RoomTableId = int64(room.ID)

	err = rc.RecorderModel.SendMsgToRecorder(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

// HandleRecorderEvents handles events coming from the recorder.
func (rc *RecorderController) HandleRecorderEvents(c *fiber.Ctx) error {
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
		rc.RecordingModel.HandleRecorderResp(req, roomInfo)
	}

	return c.SendStatus(fiber.StatusOK)
}
