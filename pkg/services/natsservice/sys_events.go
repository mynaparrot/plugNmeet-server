package natsservice

import (
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"google.golang.org/protobuf/proto"
)

func (s *NatsService) BroadcastSystemEventToRoom(event plugnmeet.NatsMsgServerToClientEvents, roomId string, data interface{}, toUserId *string) error {
	var msg string
	var err error

	switch v := data.(type) {
	case int:
	case float64:
		msg = fmt.Sprintf("%v", v)
	case []byte:
		msg = string(v)
	case string:
		msg = v
	case proto.Message:
		msg, err = s.MarshalToProtoJson(v)
		if err != nil {
			return err
		}
	default:
		return errors.New("invalid data type")
	}

	payload := plugnmeet.NatsMsgServerToClient{
		Id:    uuid.NewString(),
		Event: event,
		Msg:   msg,
	}
	message, err := proto.Marshal(&payload)
	if err != nil {
		return err
	}

	sub := fmt.Sprintf("%s:%s.system", roomId, s.app.NatsInfo.Subjects.SystemPublic)
	if toUserId != nil {
		sub = fmt.Sprintf("%s:%s.%s.system", roomId, s.app.NatsInfo.Subjects.SystemPrivate, *toUserId)
	}

	_, err = s.app.JetStream.Publish(s.ctx, sub, message)
	if err != nil {
		return err
	}

	return nil
}
