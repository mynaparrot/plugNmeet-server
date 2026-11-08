package models

import (
	"errors"
	"net/url"
	"strings"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
)

// validateExternalUrl accepts only absolute http(s) urls for external links,
// media playback, so schemes like javascript:, data: or file: are refused.
func validateExternalUrl(rawUrl string) error {
	parsedURL, err := url.ParseRequestURI(rawUrl)
	if err != nil {
		return err
	}
	scheme := strings.ToLower(parsedURL.Scheme)
	if (scheme != "http" && scheme != "https") || parsedURL.Host == "" {
		return errors.New("only http and https URLs are allowed")
	}
	return nil
}

func (m *RoomModel) HandleExternalDisplayTask(req *plugnmeet.ExternalDisplayLinkReq) (plugnmeet.StatusCode, error) {
	switch req.Task {
	case plugnmeet.ExternalDisplayLinkTask_START_EXTERNAL_LINK:
		return m.startExternalDisplay(req)
	case plugnmeet.ExternalDisplayLinkTask_STOP_EXTERNAL_LINK:
		return m.endExternalDisplay(req)
	}

	m.logger.WithFields(logrus.Fields{
		"roomId": req.RoomId,
		"task":   req.Task.String(),
	}).Warnln("invalid external display task")
	return plugnmeet.StatusCode_INVALID_PARAMETERS, config.ErrRoomInvalidRequest
}

func (m *RoomModel) startExternalDisplay(req *plugnmeet.ExternalDisplayLinkReq) (plugnmeet.StatusCode, error) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": req.RoomId,
		"userId": req.UserId,
		"url":    req.GetUrl(),
		"method": "startExternalDisplay",
	})
	log.Infoln("request to start external display link")

	if req.Url == nil || *req.Url == "" {
		log.Warnln("valid url required")
		return plugnmeet.StatusCode_INVALID_PARAMETERS, config.ErrRoomInvalidUrl
	}
	if err := validateExternalUrl(*req.Url); err != nil {
		log.WithError(err).Warnln("invalid external display url")
		return plugnmeet.StatusCode_INVALID_PARAMETERS, config.ErrRoomInvalidUrl
	}

	opts := &updateRoomMetadataOpts{
		isActive: new(true),
		url:      req.Url,
		sharedBy: &req.UserId,
	}
	return m.updateExternalDisplayRoomMetadata(req.RoomId, opts, log)
}

func (m *RoomModel) endExternalDisplay(req *plugnmeet.ExternalDisplayLinkReq) (plugnmeet.StatusCode, error) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": req.RoomId,
		"userId": req.UserId,
		"method": "endExternalDisplay",
	})
	log.Infoln("request to end external display link")

	opts := &updateRoomMetadataOpts{
		isActive: new(false),
	}
	return m.updateExternalDisplayRoomMetadata(req.RoomId, opts, log)
}

func (m *RoomModel) updateExternalDisplayRoomMetadata(roomId string, opts *updateRoomMetadataOpts, log *logrus.Entry) (plugnmeet.StatusCode, error) {
	log.Info("updating room metadata for external display")
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

	if err != nil {
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, config.ErrRoomUnexpectedError
	}

	log.Info("Successfully updated room metadata")
	return plugnmeet.StatusCode_SUCCESS, nil
}
