package models

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

func (m *BreakoutRoomModel) EndBreakoutRoom(r *plugnmeet.EndBreakoutRoomReq) error {
	_, err := m.fetchBreakoutRoom(r.RoomId, r.BreakoutRoomId)
	if err != nil {
		return err
	}
	_, _ = m.rm.EndRoom(&plugnmeet.RoomEndReq{RoomId: r.BreakoutRoomId})

	_ = m.natsService.DeleteBreakoutRoom(r.RoomId, r.BreakoutRoomId)
	m.performPostHookTask(r.RoomId)
	return nil
}

func (m *BreakoutRoomModel) EndAllBreakoutRoomsByParentRoomId(parentRoomId string) error {
	rooms, err := m.fetchBreakoutRooms(parentRoomId)
	if err != nil {
		return err
	}

	if rooms == nil || len(rooms) == 0 {
		return m.updateParentRoomMetadata(parentRoomId)
	}

	for _, r := range rooms {
		_ = m.EndBreakoutRoom(&plugnmeet.EndBreakoutRoomReq{
			BreakoutRoomId: r.Id,
			RoomId:         parentRoomId,
		})
	}
	return nil
}

func (m *BreakoutRoomModel) performPostHookTask(roomId string) {
	if c, err := m.natsService.CountBreakoutRooms(roomId); err == nil && c == 0 {
		// no room left so, delete breakoutRoomKey key for this room
		m.natsService.DeleteAllBreakoutRoomsByParentRoomId(roomId)
		_ = m.updateParentRoomMetadata(roomId)
	}
}

func (m *BreakoutRoomModel) updateParentRoomMetadata(parentRoomId string) error {
	// if no rooms left, then we can update metadata
	meta, err := m.natsService.GetRoomMetadataStruct(parentRoomId)
	if err != nil {
		return err
	}
	if meta == nil {
		// indicating room was ended
		return nil
	}

	if !meta.RoomFeatures.BreakoutRoomFeatures.IsActive {
		return nil
	}

	meta.RoomFeatures.BreakoutRoomFeatures.IsActive = false
	err = m.natsService.UpdateAndBroadcastRoomMetadata(parentRoomId, meta)
	if err != nil {
		return err
	}

	return nil
}

func (m *BreakoutRoomModel) PostTaskAfterRoomEndWebhook(roomId, metadata string) error {
	if metadata == "" {
		return nil
	}
	meta, err := m.natsService.UnmarshalRoomMetadata(metadata)
	if err != nil {
		return err
	}

	if meta.IsBreakoutRoom {
		_ = m.natsService.DeleteBreakoutRoom(meta.ParentRoomId, roomId)
		m.performPostHookTask(meta.ParentRoomId)
	} else {
		err = m.EndAllBreakoutRoomsByParentRoomId(roomId)
		if err != nil {
			return err
		}
	}

	return nil
}
