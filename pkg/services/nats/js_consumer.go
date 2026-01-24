package natsservice

import (
	"fmt"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go/jetstream"
)

// CreateUserConsumer creates a single consumer per user for chat, public, and private system messages.
func (s *NatsService) CreateUserConsumer(roomId, userId string) (jwt.StringList, error) {
	durableName := fmt.Sprintf("%s_%s", roomId, userId)
	_, err := s.js.CreateOrUpdateConsumer(s.ctx, PnmRoomStream, jetstream.ConsumerConfig{
		Durable:       durableName,
		DeliverPolicy: jetstream.DeliverNewPolicy,
		FilterSubjects: []string{
			// e.g., "chat.room123.>"
			fmt.Sprintf("%s.%s.>", s.app.NatsInfo.Subjects.Chat, roomId),
			// e.g., "sysPublic.room123.>"
			fmt.Sprintf("%s.%s.>", s.app.NatsInfo.Subjects.SystemPublic, roomId),
			// e.g., "sysPrivate.room123.user456.>"
			fmt.Sprintf("%s.%s.%s.>", s.app.NatsInfo.Subjects.SystemPrivate, roomId, userId),
		},
	})
	if err != nil {
		return nil, err
	}

	permission := jwt.StringList{
		// permission for consumer (JetStream)
		fmt.Sprintf("$JS.API.CONSUMER.INFO.%s.%s", PnmRoomStream, durableName),
		fmt.Sprintf("$JS.API.CONSUMER.MSG.NEXT.%s.%s", PnmRoomStream, durableName),
		fmt.Sprintf("$JS.ACK.%s.%s.>", PnmRoomStream, durableName),

		// permission to publish chat message (JetStream)
		fmt.Sprintf("%s.%s.%s", s.app.NatsInfo.Subjects.Chat, roomId, userId),
	}

	return permission, nil
}

func (s *NatsService) DeleteConsumer(roomId, userId string) {
	durableName := fmt.Sprintf("%s_%s", roomId, userId)
	_ = s.js.DeleteConsumer(s.ctx, PnmRoomStream, durableName)
}
