package models

import "github.com/mynaparrot/plugnmeet-protocol/plugnmeet"

func (m *ExMediaModel) endPlayBack(req *plugnmeet.ExternalMediaPlayerReq) error {
	active := new(bool)

	opts := &updateRoomMetadataOpts{
		isActive: active,
	}
	return m.updateRoomMetadata(req.RoomId, opts)
}
