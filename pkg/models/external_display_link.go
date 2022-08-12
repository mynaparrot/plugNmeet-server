package models

import (
	"errors"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

type externalDisplayLink struct {
	rs  *RoomService
	req *plugnmeet.ExternalDisplayLinkReq
}

func NewExternalDisplayLinkModel() *externalDisplayLink {
	return &externalDisplayLink{
		rs: NewRoomService(),
	}
}

func (e *externalDisplayLink) PerformTask(req *plugnmeet.ExternalDisplayLinkReq) error {
	e.req = req
	switch req.Task {
	case plugnmeet.ExternalDisplayLinkTask_START_EXTERNAL_LINK:
		return e.start()
	case plugnmeet.ExternalDisplayLinkTask_STOP_EXTERNAL_LINK:
		return e.end()
	}

	return errors.New("not valid request")
}

func (e *externalDisplayLink) start() error {
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

func (e *externalDisplayLink) end() error {
	active := new(bool)

	opts := &updateRoomMetadataOpts{
		isActive: active,
	}
	return e.updateRoomMetadata(opts)
}

func (e *externalDisplayLink) updateRoomMetadata(opts *updateRoomMetadataOpts) error {
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

	return err
}
