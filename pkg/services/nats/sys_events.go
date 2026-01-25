package natsservice

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
)

// prepareNatsServerToClientMsg prepares a plugnmeet.NatsMsgServerToClient message for broadcasting.
func (s *NatsService) prepareNatsServerToClientMsg(event plugnmeet.NatsMsgServerToClientEvents, data interface{}) ([]byte, error) {
	var msg string
	var err error

	switch v := data.(type) {
	case int, float64:
		msg = fmt.Sprintf("%v", v)
	case []byte:
		msg = string(v)
	case string:
		msg = v
	case proto.Message:
		msg, err = s.MarshalToProtoJson(v)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("invalid data type for NATS message")
	}

	payload := plugnmeet.NatsMsgServerToClient{
		Id:    uuid.NewString(),
		Event: event,
		Msg:   msg,
	}
	return proto.Marshal(&payload)
}

// BroadcastSystemEventToRoom sends a message using JetStream for reliable, guaranteed delivery.
func (s *NatsService) BroadcastSystemEventToRoom(event plugnmeet.NatsMsgServerToClientEvents, roomId string, data interface{}, toUserId *string) error {
	message, err := s.prepareNatsServerToClientMsg(event, data)
	if err != nil {
		return err
	}

	// Default to the public system subject for the room.
	sub := fmt.Sprintf("%s.%s.system", s.app.NatsInfo.Subjects.SystemPublic, roomId)
	if toUserId != nil {
		// If a user ID is provided, target the private system subject for that user.
		sub = fmt.Sprintf("%s.%s.%s.system", s.app.NatsInfo.Subjects.SystemPrivate, roomId, *toUserId)
	}

	// Explicitly publish to our stream to ensure delivery.
	_, err = s.js.Publish(s.ctx, sub, message, jetstream.WithExpectStream(s.app.NatsInfo.RoomStreamName))
	return err
}

// BroadcastSystemPubSubEventToRoom sends a public message to everyone in the room
// using core NATS for high-performance, loss-tolerant events.
func (s *NatsService) BroadcastSystemPubSubEventToRoom(event plugnmeet.NatsMsgServerToClientEvents, roomId string, data interface{}) error {
	message, err := s.prepareNatsServerToClientMsg(event, data)
	if err != nil {
		return err
	}

	// For public, real-time events, use the core NATS publisher with the specified pattern.
	sub := fmt.Sprintf("%s.%s", s.app.NatsInfo.Subjects.SystemPublic, roomId)
	return s.nc.Publish(sub, message)
}

func (s *NatsService) BroadcastSystemEventToEveryoneExceptUserId(event plugnmeet.NatsMsgServerToClientEvents, roomId string, data interface{}, exceptUserId string) error {
	ids, err := s.GetOnlineUsersId(roomId)
	if err != nil {
		return err
	}
	if ids == nil || len(ids) == 0 {
		return config.NoOnlineUserFound
	}

	for _, id := range ids {
		if id != exceptUserId {
			go func(id string) {
				err := s.BroadcastSystemEventToRoom(event, roomId, data, &id)
				if err != nil {
					s.logger.WithError(err).Errorln("failed to broadcast system event")
				}
			}(id)
		}
	}

	return nil
}

func (s *NatsService) BroadcastSystemNotificationToRoom(roomId, msg string, msgType plugnmeet.NatsSystemNotificationTypes, withSound bool, userId *string) error {
	data := &plugnmeet.NatsSystemNotification{
		Id:        uuid.NewString(),
		Type:      msgType,
		Msg:       msg,
		SentAt:    time.Now().UnixMilli(),
		WithSound: withSound,
	}

	marshal, err := protoJsonOpts.Marshal(data)
	if err != nil {
		return err
	}

	return s.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_SYSTEM_NOTIFICATION, roomId, marshal, userId)
}

func (s *NatsService) NotifyInfoMsg(roomId, msg string, withSound bool, userId *string) error {
	return s.BroadcastSystemNotificationToRoom(roomId, msg, plugnmeet.NatsSystemNotificationTypes_NATS_SYSTEM_NOTIFICATION_INFO, withSound, userId)
}

func (s *NatsService) NotifyWarningMsg(roomId, msg string, withSound bool, userId *string) error {
	return s.BroadcastSystemNotificationToRoom(roomId, msg, plugnmeet.NatsSystemNotificationTypes_NATS_SYSTEM_NOTIFICATION_WARNING, withSound, userId)
}

func (s *NatsService) NotifyErrorMsg(roomId, msg string, userId *string) error {
	return s.BroadcastSystemNotificationToRoom(roomId, msg, plugnmeet.NatsSystemNotificationTypes_NATS_SYSTEM_NOTIFICATION_ERROR, true, userId)
}
