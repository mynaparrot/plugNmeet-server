package models

import (
	"errors"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"
)

func (m *ExDisplayModel) start(req *plugnmeet.ExternalDisplayLinkReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": req.RoomId,
		"userId": req.UserId,
		"url":    req.GetUrl(),
		"method": "startExternalDisplay",
	})
	log.Infoln("request to start external display link")

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

func (m *ExDisplayModel) end(req *plugnmeet.ExternalDisplayLinkReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": req.RoomId,
		"userId": req.UserId,
		"method": "endExternalDisplay",
	})
	log.Infoln("request to end external display link")

	active := new(bool)

	opts := &updateRoomMetadataOpts{
		isActive: active,
	}
	return m.updateRoomMetadata(req.RoomId, opts, log)
}

func (m *ExDisplayModel) updateRoomMetadata(roomId string, opts *updateRoomMetadataOpts, log *logrus.Entry) error {
	log.Info("updating room metadata for external display")
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
		roomMeta.RoomFeatures.DisplayExternalLinkFeatures.IsActive = *opts.isActive
	}
	if opts.url != nil {
		roomMeta.RoomFeatures.DisplayExternalLinkFeatures.Link = opts.url
	}
	if opts.sharedBy != nil {
		roomMeta.RoomFeatures.DisplayExternalLinkFeatures.SharedBy = opts.sharedBy
	}

	err = m.natsService.UpdateAndBroadcastRoomMetadata(roomId, roomMeta)
	if err != nil {
		log.WithError(err).Error("failed to update and broadcast room metadata")
	}

	// send analytics
	val := plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_STARTED.String()
	d := &plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_EXTERNAL_DISPLAY_LINK_STATUS,
		RoomId:    roomId,
		HsetValue: &val,
	}
	if !roomMeta.RoomFeatures.DisplayExternalLinkFeatures.IsActive {
		val = plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_ENDED.String()
		d.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_EXTERNAL_DISPLAY_LINK_STATUS
		d.HsetValue = &val
	}

	m.analyticsModel.HandleEvent(d)

	if err == nil {
		log.Info("successfully updated room metadata")
	}

	return err
}
