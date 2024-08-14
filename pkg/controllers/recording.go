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
		return utils.SendCommonProtobufResponse(c, false, "only admin can start recording")
	}

	if roomId == "" {
		return utils.SendCommonProtobufResponse(c, false, "no roomId in token")
	}

	req := new(plugnmeet.RecordingReq)
	m := models.NewRecorderModel()

	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	err = req.Validate()
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	// now need to check if meeting is running or not
	rm := models.NewRoomModel()
	room, _ := rm.GetRoomInfo("", req.Sid, 1)

	if room.Id == 0 {
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

	if room.IsActiveRTMP == 1 && req.Task == plugnmeet.RecordingTasks_START_RTMP {
		return utils.SendCommonProtobufResponse(c, false, "notifications.rtmp-already-running")
	} else if room.IsActiveRTMP == 0 && req.Task == plugnmeet.RecordingTasks_STOP_RTMP {
		return utils.SendCommonProtobufResponse(c, false, "notifications.rtmp-not-running")
	}

	req.RoomId = room.RoomId
	req.RoomTableId = room.Id
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
	m := models.NewRecorderModel()

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
	rm := models.NewRoomModel()
	room, _ := rm.GetRoomInfo("", req.Sid, 1)

	if room.Id == 0 {
		return utils.SendCommonProtobufResponse(c, false, "room isn't running")
	}

	if room.RoomId != roomId {
		return utils.SendCommonProtobufResponse(c, false, "roomId in token mismatched")
	}

	if room.IsActiveRTMP == 1 && req.Task == plugnmeet.RecordingTasks_START_RTMP {
		return utils.SendCommonProtobufResponse(c, false, "RTMP broadcasting already running")
	} else if room.IsActiveRTMP == 0 && req.Task == plugnmeet.RecordingTasks_STOP_RTMP {
		return utils.SendCommonProtobufResponse(c, false, "RTMP broadcasting not running")
	}

	req.RoomId = room.RoomId
	req.RoomTableId = room.Id
	err = m.SendMsgToRecorder(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
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
		rm := models.NewRoomModel()
		roomInfo, _ := rm.GetRoomInfoByTableId(req.RoomTableId)
		if roomInfo == nil {
			return c.SendStatus(fiber.StatusNotFound)
		}

		req.RoomId = roomInfo.RoomId
		req.RoomSid = roomInfo.Sid
		m.HandleRecorderResp(req, roomInfo)
	}

	return c.SendStatus(fiber.StatusOK)
}
