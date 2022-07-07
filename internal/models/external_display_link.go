package models

import "errors"

type externalDisplayLink struct {
	rs  *RoomService
	req *ExternalDisplayLinkReq
}

func NewExternalDisplayLinkModel() *externalDisplayLink {
	return &externalDisplayLink{
		rs: NewRoomService(),
	}
}

type ExternalDisplayLinkReq struct {
	Task   string `json:"task" validate:"required"`
	Url    string `json:"url,omitempty"`
	RoomId string
	UserId string
}

func (e *externalDisplayLink) PerformTask(req *ExternalDisplayLinkReq) error {
	e.req = req
	switch req.Task {
	case "start":
		return e.start()
	case "end":
		return e.end()
	}

	return errors.New("not valid request")
}

func (e *externalDisplayLink) start() error {
	if e.req.Url == "" {
		return errors.New("valid url required")
	}
	active := new(bool)
	*active = true

	opts := &updateRoomMetadataOpts{
		isActive: active,
		url:      &e.req.Url,
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
		roomMeta.Features.DisplayExternalLinkFeatures.IsActive = *opts.isActive
	}
	if opts.url != nil {
		roomMeta.Features.DisplayExternalLinkFeatures.Link = *opts.url
	}
	if opts.sharedBy != nil {
		roomMeta.Features.DisplayExternalLinkFeatures.SharedBy = *opts.sharedBy
	}

	_, err = e.rs.UpdateRoomMetadataByStruct(e.req.RoomId, roomMeta)

	return err
}
