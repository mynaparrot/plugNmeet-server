package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
)

func HandleRecording(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")

	if isAdmin != true {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "only admin can start recording",
		})
	}

	if roomId == "" {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "no roomId in token",
		})
	}

	req := new(plugnmeet.RecordingReq)
	m := models.NewRecordingModel()

	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	err = req.Validate()
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	// now need to check if meeting is running or not
	rm := models.NewRoomModel()
	room, _ := rm.GetRoomInfo("", req.Sid, 1)

	if room.Id == 0 {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "notifications.room-not-active",
		})
	}

	if room.RoomId != roomId {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "roomId in token mismatched",
		})
	}

	if room.IsRecording == 1 && req.Task == plugnmeet.RecordingTasks_START_RECORDING {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "notifications.recording-already-running",
		})
	} else if room.IsRecording == 0 && req.Task == plugnmeet.RecordingTasks_STOP_RECORDING {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "notifications.recording-not-running",
		})
	}

	// we need to get custom design value
	m.RecordingReq = req
	err = m.SendMsgToRecorder(req.Task, room.RoomId, room.Sid, nil)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"status": true,
		"msg":    "success",
	})
}

func HandleRTMP(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")

	if isAdmin != true {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "only admin can start recording",
		})
	}

	if roomId == "" {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "no roomId in token",
		})
	}

	// we can use same as RecordingReq
	req := new(plugnmeet.RecordingReq)
	m := models.NewRecordingModel()

	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	err = req.Validate()
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	if req.Task == plugnmeet.RecordingTasks_START_RTMP {
		if req.RtmpUrl == nil {
			return c.JSON(fiber.Map{
				"status": false,
				"msg":    "rtmp url require",
			})
		} else if *req.RtmpUrl == "" {
			return c.JSON(fiber.Map{
				"status": false,
				"msg":    "rtmp url require",
			})
		}
	}

	// now need to check if meeting is running or not
	rm := models.NewRoomModel()
	room, _ := rm.GetRoomInfo("", req.Sid, 1)

	if room.Id == 0 {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "room isn't running",
		})
	}

	if room.RoomId != roomId {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "roomId in token mismatched",
		})
	}

	if room.IsActiveRTMP == 1 && req.Task == plugnmeet.RecordingTasks_START_RTMP {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "RTMP broadcasting already running",
		})
	} else if room.IsActiveRTMP == 0 && req.Task == plugnmeet.RecordingTasks_STOP_RTMP {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "RTMP broadcasting not running",
		})
	}

	// we need to get custom design value
	m.RecordingReq = req
	err = m.SendMsgToRecorder(req.Task, room.RoomId, room.Sid, req.RtmpUrl)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"status": true,
		"msg":    "success",
	})
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
