package models

import (
	"errors"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

type ExternalDisplayLink struct {
	rs             *RoomService
	req            *plugnmeet.ExternalDisplayLinkReq
	analyticsModel *AnalyticsModel
}

func NewExternalDisplayLinkModel() *ExternalDisplayLink {
	return &ExternalDisplayLink{
		rs:             NewRoomService(),
		analyticsModel: NewAnalyticsModel(),
	}
}

func (e *ExternalDisplayLink) PerformTask(req *plugnmeet.ExternalDisplayLinkReq) error {
	e.req = req
	switch req.Task {
	case plugnmeet.ExternalDisplayLinkTask_START_EXTERNAL_LINK:
		return e.start()
	case plugnmeet.ExternalDisplayLinkTask_STOP_EXTERNAL_LINK:
		return e.end()
	}

	return errors.New("not valid request")
}

func (e *ExternalDisplayLink) start() error {
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

func (e *ExternalDisplayLink) end() error {
	active := new(bool)

	opts := &updateRoomMetadataOpts{
		isActive: active,
	}
	return e.updateRoomMetadata(opts)
}

func (e *ExternalDisplayLink) updateRoomMetadata(opts *updateRoomMetadataOpts) error {
	_, roomMeta, err := e.rs.LoadRoomWithMetadata(e.req.RoomId)
	if err != nil {
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

	_, err = e.rs.UpdateRoomMetadataByStruct(e.req.RoomId, roomMeta)

	// send analytics
	d := &plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_EXTERNAL_DISPLAY_LINK_STARTED,
		RoomId:    &e.req.RoomId,
	}
	if !roomMeta.RoomFeatures.DisplayExternalLinkFeatures.IsActive {
		d.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_EXTERNAL_DISPLAY_LINK_ENDED
	}
	e.analyticsModel.HandleEvent(d)

	return err
}
