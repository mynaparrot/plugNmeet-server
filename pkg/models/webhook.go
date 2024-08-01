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
	"time"
)

type webhookEvent struct {
	rc             *redis.Client
	ctx            context.Context
	event          *livekit.WebhookEvent
	roomModel      *RoomModel
	roomService    *RoomService
	recorderModel  *RecorderModel
	notifier       *WebhookNotifier
	analyticsModel *AnalyticsModel
	rmDuration     *RoomDurationModel
}

func NewWebhookModel(e *livekit.WebhookEvent) {
	w := &webhookEvent{
		rc:             config.GetConfig().RDS,
		ctx:            context.Background(),
		event:          e,
		roomModel:      NewRoomModel(),
		roomService:    NewRoomService(),
		recorderModel:  NewRecorderModel(),
		analyticsModel: NewAnalyticsModel(),
		rmDuration:     NewRoomDurationModel(),
		notifier:       GetWebhookNotifier(),
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
	if event.Room == nil {
		log.Errorln("empty roomInfo")
		return
	}

	// as livekit sent webhook instantly but our jobs may be in progress
	// we'll check if this room is still under progress or not
	w.roomService.CheckAndWaitUntilRoomCreationInProgress(event.Room.GetName())

	rm, _ := w.roomModel.GetRoomInfo(event.Room.GetName(), "", 1)
	if rm == nil || rm.Id == 0 {
		if config.GetConfig().Client.Debug {
			// then we can allow creating room
			// we'll only create if not exist
			room := &RoomInfo{
				RoomId:       event.Room.GetName(),
				Sid:          event.Room.GetSid(),
				IsRunning:    1,
				CreationTime: event.Room.GetCreationTime(),
				Created:      time.Now().UTC().Format("2006-01-02 15:04:05"),
			}
			_, err := w.roomModel.InsertOrUpdateRoomData(room, false)
			if err != nil {
				log.Errorln(err)
				return
			}
		} else {
			// in production, we should not allow processing further
			// because may be the room was created in livekit
			// but our DB was not updated because of error
			return
		}
	}

	// may be during room creation sid was not added
	// we'll check and update during production mood
	if !config.GetConfig().Client.Debug {
		if rm.Sid == "" {
			rm.Sid = event.Room.GetSid()
			// just to update
			rm.CreationTime = event.Room.GetCreationTime()
			rm.Created = time.Now().UTC().Format("2006-01-02 15:04:05")

			_, err := w.roomModel.InsertOrUpdateRoomData(rm, true)
			if err != nil {
				log.Errorln(err)
				return
			}
		}
	}

	// now we'll insert this session in the active sessions list
	_, err := w.roomService.ManageActiveRoomsWithMetadata(event.Room.Name, "add", event.Room.Metadata)
	if err != nil {
		log.Errorln(err)
	}

	if event.Room.GetMetadata() != "" {
		info, err := w.roomService.UnmarshalRoomMetadata(event.Room.Metadata)
		if err == nil {
			info.StartedAt = uint64(time.Now().Unix())
			if info.RoomFeatures.GetRoomDuration() > 0 {
				// we'll add room info in map
				err := w.rmDuration.AddRoomWithDurationInfo(event.Room.Name, RoomDurationInfo{
					Duration:  info.RoomFeatures.GetRoomDuration(),
					StartedAt: info.GetStartedAt(),
				})
				if err != nil {
					log.Errorln(err)
				}
			}
			if info.IsBreakoutRoom {
				bm := NewBreakoutRoomModel()
				err := bm.PostTaskAfterRoomStartWebhook(event.Room.Name, info)
				if err != nil {
					log.Errorln(err)
				}
			}
			lk, err := w.roomService.UpdateRoomMetadataByStruct(event.Room.Name, info)
			if err != nil {
				log.Errorln(err)
			}
			if lk.GetMetadata() != "" {
				// use updated metadata
				event.Room.Metadata = lk.GetMetadata()
			}
		}
	}

	// for room_started event we should send webhook at the end
	// otherwise some of the services may not be ready
	w.notifier.RegisterWebhook(event.Room.GetName(), event.Room.GetSid())
	// webhook notification
	go w.sendToWebhookNotifier(event)
}

func (w *webhookEvent) roomFinished() {
	event := w.event
	if event.Room == nil {
		log.Errorln("empty roomInfo")
		return
	}

	if event.Room.GetSid() != "" {
		// we will only update the table if the SID is not empty
		room := &RoomInfo{
			Sid:       event.Room.GetSid(),
			IsRunning: 0,
			Ended:     time.Now().UTC().Format("2006-01-02 15:04:05"),
		}
		_, err := w.roomModel.UpdateRoomStatus(room)
		if err != nil {
			log.Errorln(err)
		}
	}

	go func() {
		// we are introducing a new event name here
		// because for our case we still have remaining tasks
		w.sendCustomTypeWebhook(event, "session_ended")
	}()

	// now we'll perform a few service related tasks
	time.Sleep(config.WaitBeforeTriggerOnAfterRoomEnded)
	w.roomService.OnAfterRoomClosed(event.Room.GetName())

	//we'll send a message to the recorder to stop
	err := w.recorderModel.SendMsgToRecorder(&plugnmeet.RecordingReq{
		Task:   plugnmeet.RecordingTasks_STOP,
		Sid:    w.event.Room.Sid,
		RoomId: w.event.Room.Name,
	})
	if err != nil {
		log.Errorln(err)
	}

	// few related task can be done in separate goroutine
	go w.onAfterRoomFinishedTasks(event)

	// at the end we'll handle event notification
	go func() {
		// send first
		w.sendToWebhookNotifier(event)
		// now clean up
		err := w.notifier.DeleteWebhook(event.Room.GetName())
		if err != nil {
			log.Errorln(err)
		}
	}()
}

func (w *webhookEvent) participantJoined() {
	event := w.event
	if event.Room == nil {
		log.Errorln("empty roomInfo")
		return
	}

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

	// webhook notification
	go w.sendToWebhookNotifier(event)

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
	if event.Room == nil {
		log.Errorln("empty roomInfo")
		return
	}

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

	// webhook notification
	go w.sendToWebhookNotifier(event)

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
	if w.event.Room == nil {
		log.Errorln("empty roomInfo", w.event)
		return
	}
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
	if w.event.Room == nil {
		log.Errorln("empty roomInfo", w.event)
		return
	}
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

func (w *webhookEvent) onAfterRoomFinishedTasks(event *livekit.WebhookEvent) {
	// Delete all the files those may upload during session
	if !config.GetConfig().UploadFileSettings.KeepForever {
		f := NewManageFileModel(&ManageFile{
			Sid: event.Room.Sid,
		})
		err := f.DeleteRoomUploadedDir()
		if err != nil {
			log.Errorln(err)
		}
	}

	// clear chatroom from memory
	msg := &WebsocketToRedis{
		Type:   "deleteRoom",
		RoomId: event.Room.Name,
	}
	marshal, err := json.Marshal(msg)
	if err == nil {
		_, err := w.rc.Publish(w.ctx, config.UserWebsocketChannel, marshal).Result()
		if err != nil {
			log.Errorln(err)
		}
	}

	// notify to clean room from room duration
	err = w.rmDuration.DeleteRoomWithDuration(event.Room.Name)
	if err != nil {
		log.Errorln(err)
	}

	// clean shared note
	em := NewEtherpadModel()
	_ = em.CleanAfterRoomEnd(event.Room.Name, event.Room.Metadata)

	// clean polls
	pm := NewPollsModel()
	err = pm.CleanUpPolls(event.Room.Name)
	if err != nil {
		log.Errorln(err)
	}

	// remove all breakout rooms
	bm := NewBreakoutRoomModel()
	err = bm.PostTaskAfterRoomEndWebhook(event.Room.Name, event.Room.Metadata)
	if err != nil {
		log.Errorln(err)
	}

	// speech service clean up
	sm := NewSpeechServices()
	// don't need to worry about room sid changes, because we'll compare both
	err = sm.OnAfterRoomEnded(event.Room.Name, event.Room.Sid)
	if err != nil {
		log.Errorln(err)
	}

	// finally, create the analytics file
	w.analyticsModel.PrepareToExportAnalytics(event.Room.Name, event.Room.Sid, event.Room.Metadata)
}

func (w *webhookEvent) sendToWebhookNotifier(event *livekit.WebhookEvent) {
	if event == nil || w.notifier == nil {
		return
	}
	if event.Room == nil {
		log.Errorln("empty room info for event: ", event.GetEvent())
		return
	}

	msg := utils.PrepareCommonWebhookNotifyEvent(event)
	err := w.notifier.SendWebhookEvent(msg)
	if err != nil {
		log.Errorln(err)
	}
}

func (w *webhookEvent) sendCustomTypeWebhook(event *livekit.WebhookEvent, eventName string) {
	if event == nil || w.notifier == nil {
		return
	}
	if event.Room == nil {
		log.Errorln("empty room info for event: ", event.GetEvent())
		return
	}

	msg := utils.PrepareCommonWebhookNotifyEvent(event)
	msg.Event = &eventName
	err := w.notifier.SendWebhookEvent(msg)
	if err != nil {
		log.Errorln(err)
	}
}
