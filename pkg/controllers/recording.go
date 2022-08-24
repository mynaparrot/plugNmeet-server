package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
)

func HandleRecording(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")

	if isAdmin != true {
		return utils.SendCommonResponse(c, false, "only admin can start recording")
	}

	if roomId == "" {
		return utils.SendCommonResponse(c, false, "no roomId in token")
	}

	req := new(plugnmeet.RecordingReq)
	m := models.NewRecordingModel()

	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonResponse(c, false, err.Error())
	}

	err = req.Validate()
	if err != nil {
		return utils.SendCommonResponse(c, false, err.Error())
	}

	// now need to check if meeting is running or not
	rm := models.NewRoomModel()
	room, _ := rm.GetRoomInfo("", req.Sid, 1)

	if room.Id == 0 {
		return utils.SendCommonResponse(c, false, "notifications.room-not-active")
	}

	if room.RoomId != roomId {
		return utils.SendCommonResponse(c, false, "roomId in token mismatched")
	}

	if room.IsRecording == 1 && req.Task == plugnmeet.RecordingTasks_START_RECORDING {
		return utils.SendCommonResponse(c, false, "notifications.recording-already-running")
	} else if room.IsRecording == 0 && req.Task == plugnmeet.RecordingTasks_STOP_RECORDING {
		return utils.SendCommonResponse(c, false, "notifications.recording-not-running")
	}

	if room.IsActiveRTMP == 1 && req.Task == plugnmeet.RecordingTasks_START_RTMP {
		return utils.SendCommonResponse(c, false, "notifications.rtmp-already-running")
	} else if room.IsActiveRTMP == 0 && req.Task == plugnmeet.RecordingTasks_STOP_RTMP {
		return utils.SendCommonResponse(c, false, "notifications.rtmp-not-running")
	}

	// we need to get custom design value
	m.RecordingReq = req
	err = m.SendMsgToRecorder(req.Task, room.RoomId, room.Sid, nil)
	if err != nil {
		return utils.SendCommonResponse(c, false, err.Error())
	}

	return utils.SendCommonResponse(c, true, "success")
}

func HandleRTMP(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")

	if isAdmin != true {
		return utils.SendCommonResponse(c, false, "only admin can start recording")
	}

	if roomId == "" {
		return utils.SendCommonResponse(c, false, "no roomId in token")
	}

	// we can use same as RecordingReq
	req := new(plugnmeet.RecordingReq)
	m := models.NewRecordingModel()

	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonResponse(c, false, err.Error())
	}

	err = req.Validate()
	if err != nil {
		return utils.SendCommonResponse(c, false, err.Error())
	}

	if req.Task == plugnmeet.RecordingTasks_START_RTMP {
		if req.RtmpUrl == nil {
			return utils.SendCommonResponse(c, false, "rtmp url require")
		} else if *req.RtmpUrl == "" {
			return utils.SendCommonResponse(c, false, "rtmp url require")
		}
	}

	// now need to check if meeting is running or not
	rm := models.NewRoomModel()
	room, _ := rm.GetRoomInfo("", req.Sid, 1)

	if room.Id == 0 {
		return utils.SendCommonResponse(c, false, "room isn't running")
	}

	if room.RoomId != roomId {
		return utils.SendCommonResponse(c, false, "roomId in token mismatched")
	}

	if room.IsActiveRTMP == 1 && req.Task == plugnmeet.RecordingTasks_START_RTMP {
		return utils.SendCommonResponse(c, false, "RTMP broadcasting already running")
	} else if room.IsActiveRTMP == 0 && req.Task == plugnmeet.RecordingTasks_STOP_RTMP {
		return utils.SendCommonResponse(c, false, "RTMP broadcasting not running")
	}

	// we need to get custom design value
	m.RecordingReq = req
	err = m.SendMsgToRecorder(req.Task, room.RoomId, room.Sid, req.RtmpUrl)
	if err != nil {
		return utils.SendCommonResponse(c, false, err.Error())
	}

	return utils.SendCommonResponse(c, true, "success")
}

func HandleRecorderEvents(c *fiber.Ctx) error {
	req := new(plugnmeet.RecorderToPlugNmeet)
	m := models.NewRecordingModel()

	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		log.Errorln(err)
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	if req.From == "recorder" {
		m.HandleRecorderResp(req)
	}

	return c.SendStatus(fiber.StatusOK)
}
