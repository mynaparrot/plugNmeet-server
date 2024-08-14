package roommodel

import "github.com/mynaparrot/plugnmeet-protocol/plugnmeet"

func (m *RoomModel) ChangeVisibility(r *plugnmeet.ChangeVisibilityRes) (bool, string) {
	_, roomMeta, err := m.lk.LoadRoomWithMetadata(r.RoomId)
	if err != nil {
		return false, err.Error()
	}

	if r.VisibleWhiteBoard != nil {
		roomMeta.RoomFeatures.WhiteboardFeatures.Visible = *r.VisibleWhiteBoard
	}
	if r.VisibleNotepad != nil {
		roomMeta.RoomFeatures.SharedNotePadFeatures.Visible = *r.VisibleNotepad
	}

	_, err = m.lk.UpdateRoomMetadataByStruct(r.RoomId, roomMeta)

	if err != nil {
		return false, err.Error()
	}

	return true, "success"
}
