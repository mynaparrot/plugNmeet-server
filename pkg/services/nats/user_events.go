package natsservice

import (
	"errors"
	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"time"
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

	return s.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_USER_METADATA_UPDATE, roomId, *metadata, toUser)
}

// UpdateAndBroadcastUserMetadata will update metadata & broadcast to everyone
func (s *NatsService) UpdateAndBroadcastUserMetadata(roomId, userId string, meta *plugnmeet.UserMetadata) error {
	mt, err := s.UpdateUserMetadata(userId, meta)
	if err != nil {
		return err
	}
	return s.BroadcastUserMetadata(roomId, userId, &mt, nil)
}

func (s *NatsService) SendSystemNotificationToUser(roomId, userId, msg string, msgType plugnmeet.NatsSystemNotificationTypes) error {
	data := &plugnmeet.NatsSystemNotification{
		Id:     uuid.NewString(),
		Type:   msgType,
		Msg:    msg,
		SentAt: time.Now().UnixMilli(),
	}

	marshal, err := protoJsonOpts.Marshal(data)
	if err != nil {
		return err
	}

	return s.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_SYSTEM_NOTIFICATION, roomId, marshal, &userId)
}
