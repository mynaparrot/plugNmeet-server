package models

import "github.com/mynaparrot/plugnmeet-protocol/plugnmeet"

func (m *ExDisplayModel) end(req *plugnmeet.ExternalDisplayLinkReq) error {
	active := new(bool)

	opts := &updateRoomMetadataOpts{
		isActive: active,
	}
	return m.updateRoomMetadata(req.RoomId, opts)
}
