package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
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

	req := new(models.RecordingReq)
	m := models.NewRecordingModel()

	err := c.BodyParser(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	check := m.Validation(req)
	if len(check) > 0 {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    check,
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

	if room.IsRecording == 1 && req.Task == "start-recording" {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "notifications.recording-already-running",
		})
	} else if room.IsRecording == 0 && req.Task == "stop-recording" {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "notifications.recording-not-running",
		})
	}

	// we need to get custom design value
	m.RecordingReq = req
	err = m.SendMsgToRecorder(req.Task, room.RoomId, room.Sid, "")
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
	req := new(models.RecordingReq)
	m := models.NewRecordingModel()

	err := c.BodyParser(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	check := m.Validation(req)
	if len(check) > 0 {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    check,
		})
	}

	if req.RtmpUrl == "" && req.Task == "start-rtmp" {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "rtmp url require",
		})
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

	if room.IsActiveRTMP == 1 && req.Task == "start-rtmp" {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "RTMP broadcasting already running",
		})
	} else if room.IsActiveRTMP == 0 && req.Task == "stop-rtmp" {
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
	req := new(models.RecorderResp)
	m := models.NewRecordingModel()

	err := c.BodyParser(req)
	if err != nil {
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
