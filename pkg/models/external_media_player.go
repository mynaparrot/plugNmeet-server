package models

import (
	"errors"
)

type ExternalMediaPlayer struct {
	rs  *RoomService
	req *ExternalMediaPlayerReq
}

func NewExternalMediaPlayerModel() *ExternalMediaPlayer {
	return &ExternalMediaPlayer{
		rs: NewRoomService(),
	}
}

type ExternalMediaPlayerReq struct {
	Task   string   `json:"task" validate:"required"`
	Url    string   `json:"url,omitempty"`
	SeekTo *float64 `json:"seek_to,omitempty"`
	RoomId string
	UserId string
}
type updateRoomMetadataOpts struct {
	isActive *bool
	sharedBy *string
	url      *string
}

func (e *ExternalMediaPlayer) PerformTask(req *ExternalMediaPlayerReq) error {
	e.req = req
	switch req.Task {
	case "start-playback":
		return e.startPlayBack()
	case "end-playback":
		return e.endPlayBack()
	}

	return errors.New("not valid request")
}

func (e *ExternalMediaPlayer) startPlayBack() error {
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

	return err
}
