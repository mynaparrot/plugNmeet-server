package exmediamodel

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/analyticsmodel"
)

func (m *ExMediaModel) updateRoomMetadata(roomId string, opts *updateRoomMetadataOpts) error {
	_, roomMeta, err := m.lk.LoadRoomWithMetadata(roomId)
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

	_, err = m.lk.UpdateRoomMetadataByStruct(roomId, roomMeta)

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
	analyticsModel := analyticsmodel.New(m.app, m.ds, m.rs, m.lk)
	analyticsModel.HandleEvent(d)

	return err
}
