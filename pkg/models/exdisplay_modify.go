package models

import (
	"errors"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

func (m *ExDisplayModel) start(req *plugnmeet.ExternalDisplayLinkReq) error {
	if req.Url != nil && *req.Url == "" {
		return errors.New("valid url required")
	}
	active := new(bool)
	*active = true

	opts := &updateRoomMetadataOpts{
		isActive: active,
		url:      req.Url,
		sharedBy: &req.UserId,
	}
	return m.updateRoomMetadata(req.RoomId, opts)
}

func (m *ExDisplayModel) end(req *plugnmeet.ExternalDisplayLinkReq) error {
	active := new(bool)

	opts := &updateRoomMetadataOpts{
		isActive: active,
	}
	return m.updateRoomMetadata(req.RoomId, opts)
}

func (m *ExDisplayModel) updateRoomMetadata(roomId string, opts *updateRoomMetadataOpts) error {
	roomMeta, err := m.natsService.GetRoomMetadataStruct(roomId)
	if err != nil {
		return err
	}
	if roomMeta == nil {
		return errors.New("invalid nil room metadata information")
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
		m.logger.Errorln(err)
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

	analyticsModel := NewAnalyticsModel(m.app, m.ds, m.rs, m.logger.Logger)
	analyticsModel.HandleEvent(d)

	return nil
}
