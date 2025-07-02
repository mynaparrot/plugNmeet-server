package models

import (
	"fmt"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	log "github.com/sirupsen/logrus"
)

func (m *WebhookModel) trackPublished(event *livekit.WebhookEvent) {
	if event.Room == nil || event.Track == nil {
		log.Warnln(fmt.Sprintf("invalid webhook info received: %+v", event))
		return
	}

	rInfo, err := m.natsService.GetRoomInfo(event.Room.Name)
	if err != nil || rInfo == nil {
		return
	}

	event.Room.Sid = rInfo.RoomSid
	event.Room.Metadata = rInfo.Metadata
	event.Room.MaxParticipants = uint32(rInfo.MaxParticipants)
	event.Room.EmptyTimeout = uint32(rInfo.EmptyTimeout)

	// webhook notification
	m.sendToWebhookNotifier(event)

	// send analytics
	var val string
	data := &plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_USER,
		RoomId:    event.Room.Name,
		UserId:    &event.Participant.Identity,
	}

	switch event.Track.Source {
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
	m.analyticsModel.HandleEvent(data)
}

func (m *WebhookModel) trackUnpublished(event *livekit.WebhookEvent) {
	if event.Room == nil || event.Track == nil {
		log.Warnln(fmt.Sprintf("invalid webhook info received: %+v", event))
		return
	}

	rInfo, err := m.natsService.GetRoomInfo(event.Room.Name)
	if err != nil {
		log.Errorln(err)
		return
	}
	if rInfo == nil {
		log.Errorln("empty roomInfo")
		return
	}

	event.Room.Sid = rInfo.RoomSid
	event.Room.Metadata = rInfo.Metadata
	event.Room.MaxParticipants = uint32(rInfo.MaxParticipants)
	event.Room.EmptyTimeout = uint32(rInfo.EmptyTimeout)

	// webhook notification
	m.sendToWebhookNotifier(event)

	// send analytics
	var val string
	data := &plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_USER,
		RoomId:    event.Room.Name,
		UserId:    &event.Participant.Identity,
	}

	switch event.Track.Source {
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
	m.analyticsModel.HandleEvent(data)
}
