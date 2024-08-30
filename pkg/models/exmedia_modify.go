package models

import (
	"errors"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

func (m *ExMediaModel) startPlayBack(req *plugnmeet.ExternalMediaPlayerReq) error {
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

func (m *ExMediaModel) endPlayBack(req *plugnmeet.ExternalMediaPlayerReq) error {
	active := new(bool)

	opts := &updateRoomMetadataOpts{
		isActive: active,
	}
	return m.updateRoomMetadata(req.RoomId, opts)
}

func (m *ExMediaModel) updateRoomMetadata(roomId string, opts *updateRoomMetadataOpts) error {
	roomMeta, err := m.natsService.GetRoomMetadataStruct(roomId)
	if err != nil {
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
	analyticsModel := NewAnalyticsModel(m.app, m.ds, m.rs)
	analyticsModel.HandleEvent(d)

	return err
}
