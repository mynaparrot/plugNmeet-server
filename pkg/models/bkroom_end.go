package models

import (
	"errors"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	log "github.com/sirupsen/logrus"
)

func (m *BreakoutRoomModel) EndBreakoutRoom(r *plugnmeet.EndBreakoutRoomReq) error {
	rm, err := m.natsService.GetBreakoutRoom(r.RoomId, r.BreakoutRoomId)
	if err != nil {
		return err
	}
	if rm == nil {
		return errors.New("room not found")
	}
	m.proceedToEndBkRoom(r.BreakoutRoomId, r.RoomId)
	return nil
}

func (m *BreakoutRoomModel) EndAllBreakoutRoomsByParentRoomId(parentRoomId string) error {
	ids, err := m.natsService.GetBreakoutRoomIdsByParentRoomId(parentRoomId)
	if err != nil {
		return err
	}

	if ids == nil || len(ids) == 0 {
		return m.updateParentRoomMetadata(parentRoomId)
	}

	for _, i := range ids {
		m.proceedToEndBkRoom(i, parentRoomId)
	}
	return nil
}

func (m *BreakoutRoomModel) proceedToEndBkRoom(bkRoomId, parentRoomId string) {
	ok, msg := m.rm.EndRoom(&plugnmeet.RoomEndReq{RoomId: bkRoomId})
	if !ok {
		log.Errorln(msg)
	}

	err := m.natsService.DeleteBreakoutRoom(parentRoomId, bkRoomId)
	if err != nil {
		log.Errorln(err)
	}

	m.onAfterBkRoomEnded(parentRoomId)
	// notify to the room for updating list
	_ = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_BREAKOUT_ROOM_ENDED, parentRoomId, bkRoomId, nil)
}

func (m *BreakoutRoomModel) onAfterBkRoomEnded(roomId string) {
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
		m.onAfterBkRoomEnded(meta.ParentRoomId)
	} else {
		err = m.EndAllBreakoutRoomsByParentRoomId(roomId)
		if err != nil {
			return err
		}
	}

	return nil
}
