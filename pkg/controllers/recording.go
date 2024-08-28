package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
)

func HandleRecording(c *fiber.Ctx) error {
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
	rm := dbservice.New(config.GetConfig().DB)
	isRunning := 1
	room, err := rm.GetRoomInfoBySid(req.Sid, &isRunning)
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

	m := models.NewRecorderModel(nil, nil, nil)
	err = m.SendMsgToRecorder(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

func HandleRTMP(c *fiber.Ctx) error {
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
			return utils.SendCommonProtobufResponse(c, false, "rtmp url require")
		} else if *req.RtmpUrl == "" {
			return utils.SendCommonProtobufResponse(c, false, "rtmp url require")
		}
	}

	// now need to check if meeting is running or not
	rm := dbservice.New(config.GetConfig().DB)
	isRunning := 1
	room, err := rm.GetRoomInfoBySid(req.Sid, &isRunning)
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

	m := models.NewRecorderModel(nil, nil, nil)
	err = m.SendMsgToRecorder(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

func HandleRecorderEvents(c *fiber.Ctx) error {
	req := new(plugnmeet.RecorderToPlugNmeet)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		log.Errorln(err)
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	if req.From == "recorder" {
		app := config.GetConfig()
		ds := dbservice.New(app.DB)
		roomInfo, _ := ds.GetRoomInfoByTableId(uint64(req.RoomTableId))
		if roomInfo == nil {
			return c.SendStatus(fiber.StatusNotFound)
		}

		m := models.NewRecordingModel(app, ds, nil)
		req.RoomId = roomInfo.RoomId
		req.RoomSid = roomInfo.Sid
		m.HandleRecorderResp(req, roomInfo)
	}

	return c.SendStatus(fiber.StatusOK)
}
