package natsservice

import (
	"errors"
	"fmt"

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

func (s *NatsService) BroadcastUserMetadata(roomId string, userId string, metadata, toUser *string) error {
	if metadata == nil {
		result, err := s.GetUserInfo(roomId, userId)
		if err != nil {
			return err
		}

		if result == nil {
			return fmt.Errorf("user not found")
		}
		metadata = &result.Metadata
	}

	data := &plugnmeet.NatsUserMetadataUpdate{
		Metadata: *metadata,
		UserId:   userId,
	}

	return s.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_USER_METADATA_UPDATE, roomId, data, toUser)
}

// UpdateAndBroadcastUserMetadata will update metadata & broadcast to everyone
func (s *NatsService) UpdateAndBroadcastUserMetadata(roomId, userId string, meta interface{}, toUserId *string) error {
	if meta == nil {
		return fmt.Errorf("metadata cannot be nil")
	}

	mt, err := s.UpdateUserMetadata(roomId, userId, meta)
	if err != nil {
		return err
	}
	return s.BroadcastUserMetadata(roomId, userId, &mt, toUserId)
}

func (s *NatsService) BroadcastUserInfoToRoom(event plugnmeet.NatsMsgServerToClientEvents, roomId, userId string, userInfo *plugnmeet.NatsKvUserInfo) {
	if userInfo == nil {
		info, err := s.GetUserInfo(roomId, userId)
		if err != nil {
			return
		}
		if info == nil {
			return
		}
	}

	err := s.BroadcastSystemEventToRoom(event, roomId, userInfo, nil)
	if err != nil {
		s.logger.WithError(err).Warnln("failed to broadcast user info")
	}
}
