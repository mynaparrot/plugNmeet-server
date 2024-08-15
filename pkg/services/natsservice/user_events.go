package natsservice

import (
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"google.golang.org/protobuf/proto"
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

	payload := plugnmeet.NatsMsgServerToClient{
		Id:    uuid.NewString(),
		Event: plugnmeet.NatsMsgServerToClientEvents_USER_METADATA,
		Msg:   *metadata,
	}
	message, err := proto.Marshal(&payload)
	if err != nil {
		return err
	}

	sub := fmt.Sprintf("%s:%s.system", roomId, s.app.NatsInfo.Subjects.SystemPublic)
	if toUser != nil {
		sub = fmt.Sprintf("%s:%s.%s.system", roomId, s.app.NatsInfo.Subjects.SystemPrivate, *toUser)
	}

	_, err = s.app.JetStream.Publish(s.ctx, sub, message)
	if err != nil {
		return err
	}

	return nil
}

// UpdateAndBroadcastUserMetadata will update metadata & broadcast to everyone
func (s *NatsService) UpdateAndBroadcastUserMetadata(roomId, userId string, meta *plugnmeet.UserMetadata) error {
	mt, err := s.UpdateUserInfo(userId, meta)
	if err != nil {
		return err
	}
	return s.BroadcastUserMetadata(roomId, userId, &mt, nil)
}
