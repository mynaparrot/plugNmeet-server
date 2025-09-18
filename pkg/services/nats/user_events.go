package natsservice

import (
	"fmt"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

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
