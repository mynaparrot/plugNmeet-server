package models

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
)

func (m *RoomModel) HandleExternalMediaTask(req *plugnmeet.ExternalMediaPlayerReq) (plugnmeet.StatusCode, error) {
	switch req.Task {
	case plugnmeet.ExternalMediaPlayerTask_START_PLAYBACK:
		return m.startExternalMediaPlayBack(req)
	case plugnmeet.ExternalMediaPlayerTask_END_PLAYBACK:
		return m.endExternalMediaPlayBack(req)
	}

	m.logger.WithFields(logrus.Fields{
		"roomId": req.RoomId,
		"task":   req.Task.String(),
	}).Warnln("invalid external media player task")
	return plugnmeet.StatusCode_INVALID_PARAMETERS, config.ErrRoomInvalidRequest
}

func (m *RoomModel) startExternalMediaPlayBack(req *plugnmeet.ExternalMediaPlayerReq) (plugnmeet.StatusCode, error) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": req.RoomId,
		"userId": req.UserId,
		"url":    req.GetUrl(),
		"method": "startPlayBack",
	})
	log.Infoln("request to start external media playback")

	if req.Url == nil || *req.Url == "" {
		log.Warnln("valid url required")
		return plugnmeet.StatusCode_INVALID_PARAMETERS, config.ErrRoomInvalidUrl
	}
	if err := validateExternalUrl(*req.Url); err != nil {
		log.WithError(err).Warnln("invalid external media url")
		return plugnmeet.StatusCode_INVALID_PARAMETERS, config.ErrRoomInvalidUrl
	}

	opts := &updateRoomMetadataOpts{
		isActive: new(true),
		url:      req.Url,
		sharedBy: &req.UserId,
	}
	return m.updateExternalMediaRoomMetadata(req.RoomId, opts, log)
}

func (m *RoomModel) endExternalMediaPlayBack(req *plugnmeet.ExternalMediaPlayerReq) (plugnmeet.StatusCode, error) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": req.RoomId,
		"userId": req.UserId,
		"method": "endPlayBack",
	})
	log.Infoln("request to end external media playback")

	opts := &updateRoomMetadataOpts{
		isActive: new(false),
	}
	return m.updateExternalMediaRoomMetadata(req.RoomId, opts, log)
}

func (m *RoomModel) updateExternalMediaRoomMetadata(roomId string, opts *updateRoomMetadataOpts, log *logrus.Entry) (plugnmeet.StatusCode, error) {
	log.Info("updating room metadata for external media player")
	roomMeta, err := m.natsService.GetRoomMetadataStruct(roomId)
	if err != nil {
		log.WithError(err).Error("failed to get room metadata")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, config.ErrRoomUnexpectedError
	}

	if roomMeta == nil {
		log.Error("room metadata not found")
		return plugnmeet.StatusCode_ROOM_NOT_FOUND, config.ErrRoomNotFound
	}

	if opts.isActive != nil {
		roomMeta.RoomFeatures.ExternalMediaPlayerFeatures.IsActive = *opts.isActive
	}
	if opts.url != nil {
		roomMeta.RoomFeatures.ExternalMediaPlayerFeatures.Url = opts.url
	}
	if opts.sharedBy != nil {
		roomMeta.RoomFeatures.ExternalMediaPlayerFeatures.SharedBy = opts.sharedBy
	}

	err = m.natsService.UpdateAndBroadcastRoomMetadata(roomId, roomMeta)
	if err != nil {
		log.WithError(err).Error("failed to update and broadcast room metadata")
	}

	// send analytics
	val := plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_STARTED.String()
	d := &plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_EXTERNAL_MEDIA_PLAYER_STATUS,
		RoomId:    roomId,
		HsetValue: &val,
	}
	if !roomMeta.RoomFeatures.ExternalMediaPlayerFeatures.IsActive {
		val = plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_ENDED.String()
		d.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_EXTERNAL_MEDIA_PLAYER_STATUS
		d.HsetValue = &val
	}
	m.analyticsModel.HandleEvent(d)

	if err != nil {
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, config.ErrRoomUnexpectedError
	}

	log.Info("Successfully updated room metadata")
	return plugnmeet.StatusCode_SUCCESS, nil
}
