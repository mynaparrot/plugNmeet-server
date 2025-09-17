package models

import (
	"context"
	"errors"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

func (m *BreakoutRoomModel) EndBreakoutRoom(ctx context.Context, r *plugnmeet.EndBreakoutRoomReq) error {
	rm, err := m.natsService.GetBreakoutRoom(r.RoomId, r.BreakoutRoomId)
	if err != nil {
		return err
	}
	if rm == nil {
		return errors.New("room not found")
	}
	m.proceedToEndBkRoom(ctx, r.BreakoutRoomId, r.RoomId)
	return nil
}

func (m *BreakoutRoomModel) EndAllBreakoutRoomsByParentRoomId(ctx context.Context, parentRoomId string) error {
	ids, err := m.natsService.GetBreakoutRoomIdsByParentRoomId(parentRoomId)
	if err != nil {
		return err
	}

	if ids == nil || len(ids) == 0 {
		return m.updateParentRoomMetadata(parentRoomId)
	}

	for _, i := range ids {
		m.proceedToEndBkRoom(ctx, i, parentRoomId)
	}
	return nil
}

func (m *BreakoutRoomModel) proceedToEndBkRoom(ctx context.Context, bkRoomId, parentRoomId string) {
	ok, msg := m.rm.EndRoom(ctx, &plugnmeet.RoomEndReq{RoomId: bkRoomId})
	if !ok {
		m.logger.Errorln(msg)
	}

	err := m.natsService.DeleteBreakoutRoom(parentRoomId, bkRoomId)
	if err != nil {
		m.logger.Errorln(err)
	}

	m.onAfterBkRoomEnded(parentRoomId, bkRoomId)
}

func (m *BreakoutRoomModel) onAfterBkRoomEnded(parentRoomId, bkRoomId string) {
	if c, err := m.natsService.CountBreakoutRooms(parentRoomId); err == nil && c == 0 {
		// no room left so, delete breakoutRoomKey key for this room
		m.natsService.DeleteAllBreakoutRoomsByParentRoomId(parentRoomId)
		_ = m.updateParentRoomMetadata(parentRoomId)
	}
	// notify to the room for updating list
	_ = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_BREAKOUT_ROOM_ENDED, parentRoomId, bkRoomId, nil)
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

func (m *BreakoutRoomModel) PostTaskAfterRoomEndWebhook(ctx context.Context, roomId, metadata string) error {
	if metadata == "" {
		return nil
	}
	meta, err := m.natsService.UnmarshalRoomMetadata(metadata)
	if err != nil {
		return err
	}

	if meta.IsBreakoutRoom {
		_ = m.natsService.DeleteBreakoutRoom(meta.ParentRoomId, roomId)
		m.onAfterBkRoomEnded(meta.ParentRoomId, roomId)
	} else {
		err = m.EndAllBreakoutRoomsByParentRoomId(ctx, roomId)
		if err != nil {
			return err
		}
	}

	return nil
}
