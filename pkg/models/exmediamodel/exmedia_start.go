package exmediamodel

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
