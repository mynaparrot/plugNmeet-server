package models

import (
	"context"
	"github.com/go-redis/redis/v8"
	"github.com/goccy/go-json"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	log "github.com/sirupsen/logrus"
	"time"
)

type webhookEvent struct {
	rc             *redis.Client
	ctx            context.Context
	event          *livekit.WebhookEvent
	roomModel      *roomModel
	roomService    *RoomService
	recordingModel *recordingModel
	userModel      *userModel
	notifier       *notifier
}

func NewWebhookModel(e *livekit.WebhookEvent) {
	w := &webhookEvent{
		rc:             config.AppCnf.RDS,
		ctx:            context.Background(),
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
		w.trackPublished()
	case "track_unpublished":
		w.trackUnpublished()
	}

}

func (w *webhookEvent) roomStarted() int64 {
	event := w.event

	room := &RoomInfo{
		RoomId:       event.Room.Name,
		Sid:          event.Room.Sid,
		IsRunning:    1,
		CreationTime: event.Room.CreationTime,
		Created:      time.Now().Format("2006-01-02 15:04:05"),
	}
	lastId, err := w.roomModel.InsertOrUpdateRoomData(room, false)

	if err != nil {
		log.Errorln(err)
	}

	if event.Room.Metadata != "" {
		info := new(RoomMetadata)
		err = json.Unmarshal([]byte(event.Room.Metadata), info)
		if err == nil {
			info.StartedAt = time.Now().Unix()
			if info.Features.RoomDuration > 0 {
				// we'll add room info in map
				config.AppCnf.AddRoomWithDurationMap(room.RoomId, config.RoomWithDuration{
					RoomSid:   room.Sid,
					Duration:  info.Features.RoomDuration,
					StartedAt: info.StartedAt, // we can use from livekit
				})
			}
			if info.IsBreakoutRoom {
				bm := NewBreakoutRoomModel()
				_ = bm.PostTaskAfterRoomStartWebhook(room.RoomId, info)
			}
			marshal, err := json.Marshal(info)
			if err == nil {
				_, _ = w.roomService.UpdateRoomMetadata(room.RoomId, string(marshal))
			}
		}
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
		config.AppCnf.RDS.Publish(context.Background(), "plug-n-meet-user-websocket", marshal)
	}

	// notify to clean room from room duration map
	req := new(RedisRoomDurationCheckerReq)
	req.Type = "delete"
	req.RoomId = event.Room.Name
	marshal, err = json.Marshal(req)
	if err == nil {
		w.rc.Publish(w.ctx, "plug-n-meet-room-duration-checker", marshal)
	}

	// webhook notification
	w.sendToWebhookNotifier(event)

	// clean shared note
	em := NewEtherpadModel()
	_ = em.CleanAfterRoomEnd(event.Room.Name, event.Room.Metadata)

	// clear users block list
	_, _ = w.roomService.DeleteRoomBlockList(event.Room.Name)

	// clean polls
	pm := NewPollsModel()
	_ = pm.CleanUpPolls(event.Room.Name)

	// remove all breakout rooms
	bm := NewBreakoutRoomModel()
	_ = bm.PostTaskAfterRoomEndWebhook(event.Room.Name, event.Room.Metadata)

	return affected
}

func (w *webhookEvent) participantJoined() int64 {
	event := w.event

	// webhook notification
	go w.sendToWebhookNotifier(event)

	// we won't count for recorder
	if event.Participant.Identity == "RECORDER_BOT" || event.Participant.Identity == "RTMP_BOT" {
		return 0
	}

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

	// webhook notification
	go w.sendToWebhookNotifier(event)

	// we won't count for recorder
	if event.Participant.Identity == "RECORDER_BOT" || event.Participant.Identity == "RTMP_BOT" {
		return 0
	}

	room := &RoomInfo{
		Sid: event.Room.Sid,
	}
	affected, err := w.roomModel.UpdateRoomParticipants(room, "-")
	if err != nil {
		log.Errorln(err)
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
	msg := PrepareCommonWebhookNotifyEvent(event)

	err := w.notifier.Notify(event.Room.Sid, msg)
	if err != nil {
		log.Errorln(err)
	}
}

func PrepareCommonWebhookNotifyEvent(event *livekit.WebhookEvent) *CommonNotifyEvent {
	return &CommonNotifyEvent{
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
}
