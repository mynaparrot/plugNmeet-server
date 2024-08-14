package webhookmodel

import (
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	log "github.com/sirupsen/logrus"
)

func (m *WebhookModel) trackPublished(event *livekit.WebhookEvent) {
	if event.Room == nil {
		log.Errorln("empty roomInfo", event)
		return
	}
	// webhook notification
	go m.sendToWebhookNotifier(event)

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
	if event.Room == nil {
		log.Errorln("empty roomInfo", event)
		return
	}
	// webhook notification
	go m.sendToWebhookNotifier(event)

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
