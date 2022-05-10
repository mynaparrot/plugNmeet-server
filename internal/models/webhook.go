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
	notifier       *notifier
}

func NewWebhookModel(e *livekit.WebhookEvent) {
	w := &webhookEvent{
		event:          e,
		roomModel:      NewRoomModel(),
		roomService:    NewRoomService(),
		recordingModel: NewRecordingModel(),
		userModel:      NewUserModel(),
		notifier:       NewWebhookNotifier(),
	}

	switch e.GetEvent() {
	case "room_started":
		w.roomStarted()
	case "room_finished":
		w.roomFinished()

	case "participant_joined":
		w.participantJoined()
	case "participant_left":
		w.participantLeft()

	case "track_published":
		w.participantJoined()
	case "track_unpublished":
		w.participantLeft()
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

	// webhook notification
	w.sendToWebhookNotifier(event)

	return lastId
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
		RoomId: event.Room.Name,
	}
	marshal, err := json.Marshal(msg)
	if err == nil {
		config.AppCnf.RDS.Publish(context.Background(), "plug-n-meet-websocket", marshal)
	}

	// webhook notification
	w.sendToWebhookNotifier(event)

	// clean shared note
	em := NewEtherpadModel()
	_ = em.CleanAfterRoomEnd(event.Room.Name, event.Room.Metadata)

	// clear users block list
	_, _ = w.roomService.DeleteRoomBlockList(event.Room.Name)

	return affected
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

	// webhook notification
	if !event.Participant.Permission.Hidden {
		w.sendToWebhookNotifier(event)
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

	// webhook notification
	if !event.Participant.Permission.Hidden {
		w.sendToWebhookNotifier(event)
	}

	return affected
}

func (w *webhookEvent) trackPublished() {
	// webhook notification
	w.sendToWebhookNotifier(w.event)
}

func (w *webhookEvent) trackUnpublished() {
	// webhook notification
	w.sendToWebhookNotifier(w.event)
}

func (w *webhookEvent) sendToWebhookNotifier(event *livekit.WebhookEvent) {
	msg := CommonNotifyEvent{
		Event: event.Event,
		Room: NotifyEventRoom{
			Sid:             event.Room.Sid,
			RoomId:          event.Room.Name,
			EmptyTimeout:    event.Room.EmptyTimeout,
			MaxParticipants: event.Room.MaxParticipants,
			CreationTime:    event.Room.CreationTime,
			EnabledCodecs:   event.Room.EnabledCodecs,
			Metadata:        event.Room.Metadata,
			NumParticipants: event.Room.NumParticipants,
		},
		Participant: event.Participant,
		EgressInfo:  event.EgressInfo,
		Track:       event.Track,
		Id:          event.Id,
		CreatedAt:   event.CreatedAt,
	}

	err := w.notifier.Notify(event.Room.Sid, msg)
	if err != nil {
		log.Errorln(err)
	}
}
