package models

import (
	"errors"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"
)

func (m *ExMediaModel) startPlayBack(req *plugnmeet.ExternalMediaPlayerReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": req.RoomId,
		"userId": req.UserId,
		"url":    req.GetUrl(),
		"method": "startPlayBack",
	})
	log.Infoln("request to start external media playback")

	if req.Url != nil && *req.Url == "" {
		err := errors.New("valid url required")
		log.WithError(err).Warnln()
		return err
	}
	active := new(bool)
	*active = true

	opts := &updateRoomMetadataOpts{
		isActive: active,
		url:      req.Url,
		sharedBy: &req.UserId,
	}
	return m.updateRoomMetadata(req.RoomId, opts, log)
}

func (m *ExMediaModel) endPlayBack(req *plugnmeet.ExternalMediaPlayerReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": req.RoomId,
		"userId": req.UserId,
		"method": "endPlayBack",
	})
	log.Infoln("request to end external media playback")

	active := new(bool)

	opts := &updateRoomMetadataOpts{
		isActive: active,
	}
	return m.updateRoomMetadata(req.RoomId, opts, log)
}

func (m *ExMediaModel) updateRoomMetadata(roomId string, opts *updateRoomMetadataOpts, log *logrus.Entry) error {
	log.Info("updating room metadata for external media player")
	roomMeta, err := m.natsService.GetRoomMetadataStruct(roomId)
	if err != nil {
		log.WithError(err).Error("failed to get room metadata")
		return err
	}

	if roomMeta == nil {
		err = errors.New("invalid nil room metadata information")
		log.WithError(err).Error()
		return err
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

	if err == nil {
		log.Info("successfully updated room metadata")
	}

	return err
}
