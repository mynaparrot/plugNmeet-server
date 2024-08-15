package natsservice

import (
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"google.golang.org/protobuf/proto"
)

func (s *NatsService) BroadcastRoomMetadata(roomId string, metadata, userId *string) error {
	if metadata == nil {
		result, err := s.rs.ManageActiveRoomsWithMetadata(roomId, "get", "")
		if err != nil {
			return err
		}

		if result == nil {
			return errors.New("did not found the room")
		}
		md := result[roomId]
		metadata = &md
	}

	payload := plugnmeet.NatsMsgServerToClient{
		Id:    uuid.NewString(),
		Event: plugnmeet.NatsMsgServerToClientEvents_ROOM_METADATA,
		Msg:   *metadata,
	}
	message, err := proto.Marshal(&payload)
	if err != nil {
		return err
	}

	sub := fmt.Sprintf("%s:%s.system", roomId, s.app.NatsInfo.Subjects.SystemPublic)
	if userId != nil {
		sub = fmt.Sprintf("%s:%s.%s.system", roomId, s.app.NatsInfo.Subjects.SystemPrivate, *userId)
	}

	_, err = s.app.JetStream.Publish(s.ctx, sub, message)
	if err != nil {
		return err
	}

	return nil
}

func (s *NatsService) UpdateAndBroadcastRoomMetadata(roomId string, meta *plugnmeet.RoomMetadata) error {
	metadata, err := s.MarshalRoomMetadata(meta)
	if err != nil {
		return err
	}
	_, err = s.rs.ManageActiveRoomsWithMetadata(roomId, "add", metadata)
	if err != nil {
		return err
	}
	return s.BroadcastRoomMetadata(roomId, &metadata, nil)
}
