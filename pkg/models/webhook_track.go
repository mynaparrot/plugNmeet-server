package models

import (
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"
)

func (m *WebhookModel) trackPublished(event *livekit.WebhookEvent) {
	if event.Room == nil || event.Track == nil || event.Participant == nil {
		m.logger.Warnln("received track_published webhook with nil room, track, or participant info")
		return
	}

	log := m.logger.WithFields(logrus.Fields{
		"roomId":        event.Room.Name,
		"participantId": event.Participant.Identity,
		"trackId":       event.Track.Sid,
		"event":         event.GetEvent(),
	})
	log.Infoln("handling track_published webhook")

	rInfo, err := m.natsService.GetRoomInfo(event.Room.Name)
	if err != nil {
		log.WithError(err).Errorln("failed to get room info from NATS")
		return
	}
	if rInfo == nil {
		log.Warnln("room not found in NATS, skipping track_published tasks")
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
	log.Info("successfully processed track_published webhook")
}

func (m *WebhookModel) trackUnpublished(event *livekit.WebhookEvent) {
	if event.Room == nil || event.Track == nil || event.Participant == nil {
		m.logger.Warnln("received track_unpublished webhook with nil room, track, or participant info")
		return
	}

	log := m.logger.WithFields(logrus.Fields{
		"roomId":        event.Room.Name,
		"participantId": event.Participant.Identity,
		"trackId":       event.Track.Sid,
		"event":         event.GetEvent(),
	})
	log.Infoln("handling track_unpublished webhook")

	rInfo, err := m.natsService.GetRoomInfo(event.Room.Name)
	if err != nil {
		log.WithError(err).Errorln("failed to get room info from NATS")
		return
	}
	if rInfo == nil {
		log.Warnln("room not found in NATS, skipping track_unpublished tasks")
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
	log.Info("successfully processed track_unpublished webhook")
}
