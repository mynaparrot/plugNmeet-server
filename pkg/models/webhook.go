package models

import (
	"context"
	"fmt"
	"github.com/goccy/go-json"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
	"time"
)

type webhookEvent struct {
	rc             *redis.Client
	ctx            context.Context
	event          *livekit.WebhookEvent
	roomModel      *RoomModel
	roomService    *RoomService
	recorderModel  *RecorderModel
	notifier       *WebhookNotifierModel
	analyticsModel *AnalyticsModel
	rmDuration     *RoomDurationModel
}

func NewWebhookModel(e *livekit.WebhookEvent) {
	w := &webhookEvent{
		rc:             config.AppCnf.RDS,
		ctx:            context.Background(),
		event:          e,
		roomModel:      NewRoomModel(),
		roomService:    NewRoomService(),
		recorderModel:  NewRecorderModel(),
		notifier:       NewWebhookNotifier(),
		analyticsModel: NewAnalyticsModel(),
		rmDuration:     NewRoomDurationModel(),
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

	rm, _ := w.roomModel.GetRoomInfo(event.Room.Name, event.Room.Sid, 1)
	if rm.Id == 0 {
		// we'll only create if not exist
		room := &RoomInfo{
			RoomId:       event.Room.Name,
			Sid:          event.Room.Sid,
			IsRunning:    1,
			CreationTime: event.Room.CreationTime,
			Created:      time.Now().UTC().Format("2006-01-02 15:04:05"),
		}
		_, err := w.roomModel.InsertOrUpdateRoomData(room, false)
		if err != nil {
			log.Errorln(err)
		}
	}

	// now we'll insert this session in the active sessions list
	_, err := w.roomService.ManageActiveRoomsList(event.Room.Name, "add", event.Room.CreationTime)
	if err != nil {
		log.Errorln(err)
	}

	if event.Room.Metadata != "" {
		info, err := w.roomService.UnmarshalRoomMetadata(event.Room.Metadata)
		if err == nil {
			info.StartedAt = uint64(time.Now().Unix())
			if info.RoomFeatures.RoomDuration != nil && *info.RoomFeatures.RoomDuration > 0 {
				// we'll add room info in map
				_ = w.rmDuration.AddRoomWithDurationInfo(event.Room.Name, RoomDurationInfo{
					Duration:  *info.RoomFeatures.RoomDuration,
					StartedAt: info.StartedAt, // we can use from livekit
				})
			}
			if info.IsBreakoutRoom {
				bm := NewBreakoutRoomModel()
				_ = bm.PostTaskAfterRoomStartWebhook(event.Room.Name, info)
			}
			if err == nil {
				_, _ = w.roomService.UpdateRoomMetadataByStruct(event.Room.Name, info)
			}
		}
	}
}

func (w *webhookEvent) roomFinished() {
	event := w.event
	// webhook notification
	go w.sendToWebhookNotifier(event)

	if event.Room.Sid != "" {
		// we will only update table if the SID is not empty
		room := &RoomInfo{
			Sid:       event.Room.Sid,
			IsRunning: 0,
			Ended:     time.Now().UTC().Format("2006-01-02 15:04:05"),
		}
		_, err := w.roomModel.UpdateRoomStatus(room)
		if err != nil {
			log.Errorln(err)
		}
	}
	// now we'll remove this session from the active sessions list
	_, err := w.roomService.ManageActiveRoomsList(event.Room.Name, "del", event.CreatedAt)
	if err != nil {
		log.Errorln(err)
	}
	// we'll also delete active users list for this room
	go func() {
		// let's wait few seconds so that any pending task will finish
		time.Sleep(5 * time.Second)
		_, err = w.roomService.ManageActiveUsersList(event.Room.Name, "", "delList", event.CreatedAt)
		if err != nil {
			log.Errorln(err)
		}
	}()

	//we'll send message to recorder to stop
	_ = w.recorderModel.SendMsgToRecorder(&plugnmeet.RecordingReq{
		Task:   plugnmeet.RecordingTasks_STOP,
		Sid:    w.event.Room.Sid,
		RoomId: w.event.Room.Name,
	})

	// Delete all the files those may upload during session
	go func() {
		if !config.AppCnf.UploadFileSettings.KeepForever {
			f := NewManageFileModel(&ManageFile{
				Sid: event.Room.Sid,
			})
			_ = f.DeleteRoomUploadedDir()
		}
	}()

	// clear chatroom from memory
	go func() {
		msg := &WebsocketToRedis{
			Type:   "deleteRoom",
			RoomId: event.Room.Name,
		}
		marshal, err := json.Marshal(msg)
		if err == nil {
			_, _ = w.rc.Publish(context.Background(), "plug-n-meet-user-websocket", marshal).Result()
		}
	}()

	// notify to clean room from room duration
	go func() {
		_ = w.rmDuration.DeleteRoomWithDuration(event.Room.Name)
	}()

	// clean shared note
	go func() {
		em := NewEtherpadModel()
		_ = em.CleanAfterRoomEnd(event.Room.Name, event.Room.Metadata)
	}()

	// clear users block list
	go func() {
		_, _ = w.roomService.DeleteRoomBlockList(event.Room.Name)
	}()

	// clean polls
	go func() {
		pm := NewPollsModel()
		_ = pm.CleanUpPolls(event.Room.Name)
	}()

	// remove all breakout rooms
	go func() {
		bm := NewBreakoutRoomModel()
		_ = bm.PostTaskAfterRoomEndWebhook(event.Room.Name, event.Room.Metadata)
	}()

	// speech service clean up
	go func() {
		sm := NewSpeechServices()
		// don't need to worry about room sid changes, because we'll compare both
		sm.OnAfterRoomEnded(event.Room.Name, event.Room.Sid)
	}()

	// finally create analytics file
	go w.analyticsModel.PrepareToExportAnalytics(event.Room.Sid, event.Room.Metadata)
}

func (w *webhookEvent) participantJoined() {
	event := w.event
	// we won't count for recorder
	/*if event.Participant.Identity == config.RECORDER_BOT || event.Participant.Identity == config.RTMP_BOT {
		return
	}*/

	// webhook notification
	go w.sendToWebhookNotifier(event)

	room := &RoomInfo{
		Sid: event.Room.Sid,
	}
	_, err := w.roomModel.UpdateRoomParticipants(room, "+")
	if err != nil {
		log.Errorln(err)
	}

	// now we'll add this user to active users list for this room
	_, err = w.roomService.ManageActiveUsersList(event.Room.Name, event.Participant.Identity, "add", event.Participant.JoinedAt)
	if err != nil {
		log.Errorln(err)
	}

	// send analytics
	at := fmt.Sprintf("%d", time.Now().UnixMilli())
	if event.GetCreatedAt() > 0 {
		// sometime events send in unordered way, so better to use when it was created
		// otherwise will give invalid data, for backward compatibility convert to milliseconds
		at = fmt.Sprintf("%d", event.GetCreatedAt()*1000)
	}
	w.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_JOINED,
		RoomId:    event.Room.Name,
		UserId:    &event.Participant.Identity,
		UserName:  &event.Participant.Name,
		ExtraData: &event.Participant.Metadata,
		HsetValue: &at,
	})
}

func (w *webhookEvent) participantLeft() {
	event := w.event
	// we won't count for recorder
	/*if event.Participant.Identity == config.RECORDER_BOT || event.Participant.Identity == config.RTMP_BOT {
		return
	}*/

	// webhook notification
	go w.sendToWebhookNotifier(event)

	room := &RoomInfo{
		Sid: event.Room.Sid,
	}
	_, err := w.roomModel.UpdateRoomParticipants(room, "-")
	if err != nil {
		log.Errorln(err)
	}
	// now we'll delete this user from active users list for this room
	_, err = w.roomService.ManageActiveUsersList(event.Room.Name, event.Participant.Identity, "del", event.CreatedAt)
	if err != nil {
		log.Errorln(err)
	}

	// if we missed to calculate this user's speech service usage stat
	// for sudden disconnection
	sm := NewSpeechServices()
	_ = sm.SpeechServiceUsersUsage(event.Room.Name, event.Room.Sid, event.Participant.Identity, plugnmeet.SpeechServiceUserStatusTasks_SPEECH_TO_TEXT_SESSION_ENDED)

	// send analytics
	at := fmt.Sprintf("%d", time.Now().UnixMilli())
	if event.GetCreatedAt() > 0 {
		// sometime events send in unordered way, so better to use when it was created
		// otherwise will give invalid data, for backward compatibility convert to milliseconds
		at = fmt.Sprintf("%d", event.GetCreatedAt()*1000)
	}
	w.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_USER,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_LEFT,
		RoomId:    event.Room.Name,
		UserId:    &event.Participant.Identity,
		HsetValue: &at,
	})
}

func (w *webhookEvent) trackPublished() {
	// webhook notification
	go w.sendToWebhookNotifier(w.event)

	// send analytics
	var val string
	data := &plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_USER,
		RoomId:    w.event.Room.Name,
		UserId:    &w.event.Participant.Identity,
	}

	switch w.event.Track.Source {
	case livekit.TrackSource_MICROPHONE:
		val = plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_STARTED.String()
		data.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_MIC_STATUS
	case livekit.TrackSource_CAMERA:
		val = plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_STARTED.String()
		data.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_WEBCAM_STATUS
	case livekit.TrackSource_SCREEN_SHARE,
		livekit.TrackSource_SCREEN_SHARE_AUDIO:
		val = plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_STARTED.String()
		data.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_SCREEN_SHARE_STATUS
	}
	data.HsetValue = &val
	w.analyticsModel.HandleEvent(data)
}

func (w *webhookEvent) trackUnpublished() {
	// webhook notification
	go w.sendToWebhookNotifier(w.event)

	// send analytics
	var val string
	data := &plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_USER,
		RoomId:    w.event.Room.Name,
		UserId:    &w.event.Participant.Identity,
	}

	switch w.event.Track.Source {
	case livekit.TrackSource_MICROPHONE:
		val = plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_ENDED.String()
		data.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_MIC_STATUS
	case livekit.TrackSource_CAMERA:
		val = plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_ENDED.String()
		data.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_WEBCAM_STATUS
	case livekit.TrackSource_SCREEN_SHARE,
		livekit.TrackSource_SCREEN_SHARE_AUDIO:
		val = plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_ENDED.String()
		data.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_SCREEN_SHARE_STATUS
	}
	data.HsetValue = &val
	w.analyticsModel.HandleEvent(data)
}

func (w *webhookEvent) sendToWebhookNotifier(event *livekit.WebhookEvent) {
	if event == nil {
		return
	}
	if event.Room == nil {
		log.Errorln("empty room info for event: ", event.GetEvent())
		return
	}

	msg := utils.PrepareCommonWebhookNotifyEvent(event)
	op := protojson.MarshalOptions{
		EmitUnpopulated: false,
		UseProtoNames:   true,
	}
	marshal, err := op.Marshal(msg)
	if err != nil {
		log.Errorln(err)
		return
	}
	err = w.notifier.Notify(event.Room.Sid, marshal)
	if err != nil {
		log.Errorln(err)
	}
}
