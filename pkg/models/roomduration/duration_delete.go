package roomdurationmodel

func (m *RoomDurationModel) DeleteRoomWithDuration(roomId string) error {
	err := m.rs.DeleteRoomWithDuration(roomId)
	if err != nil {
		return err
	}
	return nil
}
