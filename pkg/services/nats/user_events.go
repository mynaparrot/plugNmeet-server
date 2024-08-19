package natsservice

import (
	"errors"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	log "github.com/sirupsen/logrus"
)

func (s *NatsService) BroadcastUserMetadata(roomId string, userId string, metadata, toUser *string) error {
	if metadata == nil {
		result, err := s.GetUserInfo(userId)
		if err != nil {
			return err
		}

		if result == nil {
			return errors.New("did not found the user")
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
		return errors.New("metadata cannot be nil")
	}

	mt, err := s.UpdateUserMetadata(userId, meta)
	if err != nil {
		return err
	}
	return s.BroadcastUserMetadata(roomId, userId, &mt, toUserId)
}

func (s *NatsService) BroadcastUserInfoToRoom(event plugnmeet.NatsMsgServerToClientEvents, roomId, userId string, userInfo *plugnmeet.NatsKvUserInfo) {
	if userInfo == nil {
		info, err := s.GetUserInfo(userId)
		if err != nil {
			return
		}
		if info == nil {
			return
		}
	}

	err := s.BroadcastSystemEventToRoom(event, roomId, userInfo, nil)
	if err != nil {
		log.Warnln(err)
	}
}
