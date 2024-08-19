package roommodel

import "github.com/mynaparrot/plugnmeet-protocol/plugnmeet"

func (m *RoomModel) ChangeVisibility(r *plugnmeet.ChangeVisibilityRes) (bool, string) {
	roomMeta, err := m.natsService.GetRoomMetadataStruct(r.RoomId)
	if err != nil {
		return false, err.Error()
	}

	if r.VisibleWhiteBoard != nil {
		roomMeta.RoomFeatures.WhiteboardFeatures.Visible = *r.VisibleWhiteBoard
	}
	if r.VisibleNotepad != nil {
		roomMeta.RoomFeatures.SharedNotePadFeatures.Visible = *r.VisibleNotepad
	}

	err = m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, roomMeta)

	if err != nil {
		return false, err.Error()
	}

	return true, "success"
}
