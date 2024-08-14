package models

import (
	"errors"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

type ExternalMediaPlayer struct {
	rs             *RoomService
	req            *plugnmeet.ExternalMediaPlayerReq
	analyticsModel *AnalyticsModel
}

func NewExternalMediaPlayerModel() *ExternalMediaPlayer {
	return &ExternalMediaPlayer{
		rs:             NewRoomService(),
		analyticsModel: NewAnalyticsModel(),
	}
}

type updateRoomMetadataOpts struct {
	isActive *bool
	sharedBy *string
	url      *string
}

func (e *ExternalMediaPlayer) PerformTask(req *plugnmeet.ExternalMediaPlayerReq) error {
	e.req = req
	switch req.Task {
	case plugnmeet.ExternalMediaPlayerTask_START_PLAYBACK:
		return e.startPlayBack()
	case plugnmeet.ExternalMediaPlayerTask_END_PLAYBACK:
		return e.endPlayBack()
	}

	return errors.New("not valid request")
}

func (e *ExternalMediaPlayer) startPlayBack() error {
	if e.req.Url != nil && *e.req.Url == "" {
		return errors.New("valid url required")
	}
	active := new(bool)
	*active = true

	opts := &updateRoomMetadataOpts{
		isActive: active,
		url:      e.req.Url,
		sharedBy: &e.req.UserId,
	}
	return e.updateRoomMetadata(opts)
}

func (e *ExternalMediaPlayer) endPlayBack() error {
	active := new(bool)

	opts := &updateRoomMetadataOpts{
		isActive: active,
	}
	return e.updateRoomMetadata(opts)
}

func (e *ExternalMediaPlayer) updateRoomMetadata(opts *updateRoomMetadataOpts) error {
	_, roomMeta, err := e.rs.LoadRoomWithMetadata(e.req.RoomId)
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

	_, err = e.rs.UpdateRoomMetadataByStruct(e.req.RoomId, roomMeta)

	// send analytics
	val := plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_STARTED.String()
	d := &plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_EXTERNAL_MEDIA_PLAYER_STATUS,
		RoomId:    e.req.RoomId,
		HsetValue: &val,
	}
	if !roomMeta.RoomFeatures.ExternalMediaPlayerFeatures.IsActive {
		val = plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_ENDED.String()
		d.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_EXTERNAL_MEDIA_PLAYER_STATUS
		d.HsetValue = &val
	}
	e.analyticsModel.HandleEvent(d)

	return err
}
