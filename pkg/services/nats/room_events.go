package natsservice

import (
	"errors"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

func (s *NatsService) BroadcastRoomMetadata(roomId string, metadata, userId *string) error {
	if metadata == nil {
		rInfo, err := s.GetRoomInfo(roomId)
		if err != nil {
			return err
		}

		if rInfo == nil {
			return errors.New("did not found the room")
		}
		metadata = &rInfo.Metadata
	}

	return s.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_ROOM_METADATA_UPDATE, roomId, *metadata, userId)
}

// UpdateAndBroadcastRoomMetadata will update and broadcast to everyone
// if provided userId then only to that user
func (s *NatsService) UpdateAndBroadcastRoomMetadata(roomId string, meta interface{}) error {
	if meta == nil {
		return errors.New("metadata cannot be nil")
	}
	metadata, err := s.updateRoomMetadata(roomId, meta)
	if err != nil {
		return err
	}
	return s.BroadcastRoomMetadata(roomId, &metadata, nil)
}
