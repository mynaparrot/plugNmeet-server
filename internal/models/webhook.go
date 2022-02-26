package models

import (
	"context"
	"encoding/json"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugNmeet/internal/config"
	log "github.com/sirupsen/logrus"
	"time"
)

type webhookEvent struct {
	event          *livekit.WebhookEvent
	roomModel      *roomModel
	roomService    *RoomService
	recordingModel *recordingModel
	userModel      *userModel
}

func NewWebhookModel(e *livekit.WebhookEvent) {
	w := &webhookEvent{
		event:          e,
		roomModel:      NewRoomModel(),
		roomService:    NewRoomService(),
		recordingModel: NewRecordingModel(),
		userModel:      NewUserModel(),
	}

	switch e.GetEvent() {
	case "room_started":
		w.roomStarted()

	case "participant_joined":
		w.participantJoined()

	case "participant_left":
		w.participantLeft()

	case "room_finished":
		w.roomFinished()
	}
}

func (w *webhookEvent) roomStarted() int64 {
	event := w.event

	room := &RoomInfo{
		RoomId:       event.Room.Name,
		Sid:          event.Room.Sid,
		IsRunning:    1,
		CreationTime: event.Room.CreationTime,
	}
	lastId, err := w.roomModel.InsertRoomData(room)
	if err != nil {
		log.Errorln(err)
	}

	return lastId
}

func (w *webhookEvent) participantJoined() int64 {
	event := w.event

	room := &RoomInfo{
		Sid: event.Room.Sid,
	}
	affected, err := w.roomModel.UpdateRoomParticipants(room, "+")
	if err != nil {
		log.Errorln(err)
	}

	return affected
}

func (w *webhookEvent) participantLeft() int64 {
	event := w.event

	room := &RoomInfo{
		Sid: event.Room.Sid,
	}
	affected, err := w.roomModel.UpdateRoomParticipants(room, "-")
	if err != nil {
		log.Errorln(err)
	}

	return affected
}

func (w *webhookEvent) roomFinished() int64 {
	event := w.event

	room := &RoomInfo{
		Sid:       event.Room.Sid,
		IsRunning: 0,
		Ended:     time.Now().Format("2006-01-02 15:04:05"),
	}
	affected, err := w.roomModel.UpdateRoomStatus(room)
	if err != nil {
		log.Errorln(err)
	}

	//we'll send message to recorder to stop
	_ = w.recordingModel.SendMsgToRecorder("stop", w.event.Room.Name, w.event.Room.Sid, "")

	// finally, delete all the files those may upload during session
	if !config.AppCnf.UploadFileSettings.KeepForever {
		f := NewManageFileModel(&ManageFile{
			Sid: event.Room.Sid,
		})
		_ = f.DeleteRoomUploadedDir()
	}

	// clear chatroom from memory
	msg := WebsocketRedisMsg{
		Type:   "deleteRoom",
		RoomId: &event.Room.Name,
	}
	marshal, err := json.Marshal(msg)
	if err == nil {
		config.AppCnf.RDS.Publish(context.Background(), "plug-n-meet-websocket", marshal)
	}

	return affected
}
