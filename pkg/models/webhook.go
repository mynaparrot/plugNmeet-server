package models

import (
	"context"
	"github.com/go-redis/redis/v8"
	"github.com/goccy/go-json"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	log "github.com/sirupsen/logrus"
	"time"
)

type webhookEvent struct {
	rc             *redis.Client
	ctx            context.Context
	event          *livekit.WebhookEvent
	roomModel      *RoomModel
	roomService    *RoomService
	recordingModel *RecordingModel
	userModel      *UserModel
	notifier       *WebhookNotifierModel
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

func (w *webhookEvent) roomStarted() {
	event := w.event

	// webhook notification
	go w.sendToWebhookNotifier(event)

	room := &RoomInfo{
		RoomId:       event.Room.Name,
		Sid:          event.Room.Sid,
		IsRunning:    1,
		CreationTime: event.Room.CreationTime,
		Created:      time.Now().Format("2006-01-02 15:04:05"),
	}
	_, err := w.roomModel.InsertOrUpdateRoomData(room, false)
	if err != nil {
		log.Errorln(err)
	}

	if event.Room.Metadata != "" {
		info := new(plugnmeet.RoomMetadata)
		err = json.Unmarshal([]byte(event.Room.Metadata), info)
		if err == nil {
			info.StartedAt = uint64(time.Now().Unix())
			if info.RoomFeatures.RoomDuration != nil && *info.RoomFeatures.RoomDuration > 0 {
				// we'll add room info in map
				config.AppCnf.AddRoomWithDurationMap(room.RoomId, config.RoomWithDuration{
					RoomSid:   room.Sid,
					Duration:  *info.RoomFeatures.RoomDuration,
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
}

func (w *webhookEvent) roomFinished() {
	event := w.event

	// webhook notification
	go w.sendToWebhookNotifier(event)

	room := &RoomInfo{
		Sid:       event.Room.Sid,
		IsRunning: 0,
		Ended:     time.Now().Format("2006-01-02 15:04:05"),
	}
	_, err := w.roomModel.UpdateRoomStatus(room)
	if err != nil {
		log.Errorln(err)
	}

	//we'll send message to recorder to stop
	_ = w.recordingModel.SendMsgToRecorder(plugnmeet.RecordingTasks_STOP, w.event.Room.Name, w.event.Room.Sid, nil)

	// Delete all the files those may upload during session
	if !config.AppCnf.UploadFileSettings.KeepForever {
		go func() {
			f := NewManageFileModel(&ManageFile{
				Sid: event.Room.Sid,
			})
			_ = f.DeleteRoomUploadedDir()
		}()
	}

	// clear chatroom from memory
	msg := &WebsocketToRedis{
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

	// clean shared note
	go func() {
		em := NewEtherpadModel()
		_ = em.CleanAfterRoomEnd(event.Room.Name, event.Room.Metadata)
	}()

	// clear users block list
	_, _ = w.roomService.DeleteRoomBlockList(event.Room.Name)

	// clean polls
	pm := NewPollsModel()
	_ = pm.CleanUpPolls(event.Room.Name)

	// remove all breakout rooms
	go func() {
		bm := NewBreakoutRoomModel()
		_ = bm.PostTaskAfterRoomEndWebhook(event.Room.Name, event.Room.Metadata)
	}()
}

func (w *webhookEvent) participantJoined() {
	event := w.event
	// we won't count for recorder
	if event.Participant.Identity == config.RECORDER_BOT || event.Participant.Identity == config.RTMP_BOT {
		return
	}

	// webhook notification
	go w.sendToWebhookNotifier(event)

	room := &RoomInfo{
		Sid: event.Room.Sid,
	}
	_, err := w.roomModel.UpdateRoomParticipants(room, "+")
	if err != nil {
		log.Errorln(err)
	}
}

func (w *webhookEvent) participantLeft() {
	event := w.event
	// we won't count for recorder
	if event.Participant.Identity == config.RECORDER_BOT || event.Participant.Identity == config.RTMP_BOT {
		return
	}

	// webhook notification
	go w.sendToWebhookNotifier(event)

	room := &RoomInfo{
		Sid: event.Room.Sid,
	}
	_, err := w.roomModel.UpdateRoomParticipants(room, "-")
	if err != nil {
		log.Errorln(err)
	}
}

func (w *webhookEvent) trackPublished() {
	// webhook notification
	go w.sendToWebhookNotifier(w.event)
}

func (w *webhookEvent) trackUnpublished() {
	// webhook notification
	go w.sendToWebhookNotifier(w.event)
}

func (w *webhookEvent) sendToWebhookNotifier(event *livekit.WebhookEvent) {
	msg := utils.PrepareCommonWebhookNotifyEvent(event)

	err := w.notifier.Notify(event.Room.Sid, msg)
	if err != nil {
		log.Errorln(err)
	}
}
