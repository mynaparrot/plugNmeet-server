package natsservice

import (
	"fmt"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go/jetstream"
)

func (s *NatsService) CreateChatConsumer(roomId, userId string) (jwt.StringList, error) {
	_, err := s.js.CreateOrUpdateConsumer(s.ctx, roomId, jetstream.ConsumerConfig{
		Durable: fmt.Sprintf("%s:%s", s.app.NatsInfo.Subjects.Chat, userId),
		FilterSubjects: []string{
			fmt.Sprintf("%s:%s.>", roomId, s.app.NatsInfo.Subjects.Chat),
		},
	})
	if err != nil {
		return nil, err
	}

	permission := jwt.StringList{
		fmt.Sprintf("$JS.API.CONSUMER.INFO.%s.%s:%s", roomId, s.app.NatsInfo.Subjects.Chat, userId),
		fmt.Sprintf("$JS.API.CONSUMER.MSG.NEXT.%s.%s:%s", roomId, s.app.NatsInfo.Subjects.Chat, userId),
		fmt.Sprintf("%s:%s.%s", roomId, s.app.NatsInfo.Subjects.Chat, userId),
		fmt.Sprintf("$JS.ACK.%s.%s:%s.>", roomId, s.app.NatsInfo.Subjects.Chat, userId),
	}

	return permission, nil
}

func (s *NatsService) CreateSystemPublicConsumer(roomId, userId string) (jwt.StringList, error) {
	_, err := s.js.CreateOrUpdateConsumer(s.ctx, roomId, jetstream.ConsumerConfig{
		Durable:       fmt.Sprintf("%s:%s", s.app.NatsInfo.Subjects.SystemPublic, userId),
		DeliverPolicy: jetstream.DeliverNewPolicy,
		FilterSubjects: []string{
			fmt.Sprintf("%s:%s.>", roomId, s.app.NatsInfo.Subjects.SystemPublic),
		},
	})
	if err != nil {
		return nil, err
	}

	permission := jwt.StringList{
		fmt.Sprintf("$JS.API.CONSUMER.INFO.%s.%s:%s", roomId, s.app.NatsInfo.Subjects.SystemPublic, userId),
		fmt.Sprintf("$JS.API.CONSUMER.MSG.NEXT.%s.%s:%s", roomId, s.app.NatsInfo.Subjects.SystemPublic, userId),
		fmt.Sprintf("$JS.ACK.%s.%s:%s.>", roomId, s.app.NatsInfo.Subjects.SystemPublic, userId),
	}

	return permission, nil
}

func (s *NatsService) CreateSystemPrivateConsumer(roomId, userId string) (jwt.StringList, error) {
	_, err := s.js.CreateOrUpdateConsumer(s.ctx, roomId, jetstream.ConsumerConfig{
		Durable:       fmt.Sprintf("%s:%s", s.app.NatsInfo.Subjects.SystemPrivate, userId),
		DeliverPolicy: jetstream.DeliverNewPolicy,
		FilterSubjects: []string{
			fmt.Sprintf("%s:%s.%s.>", roomId, s.app.NatsInfo.Subjects.SystemPrivate, userId),
		},
	})
	if err != nil {
		return nil, err
	}

	permission := jwt.StringList{
		fmt.Sprintf("$JS.API.CONSUMER.INFO.%s.%s:%s", roomId, s.app.NatsInfo.Subjects.SystemPrivate, userId),
		fmt.Sprintf("$JS.API.CONSUMER.MSG.NEXT.%s.%s:%s", roomId, s.app.NatsInfo.Subjects.SystemPrivate, userId),
		fmt.Sprintf("$JS.ACK.%s.%s:%s.>", roomId, s.app.NatsInfo.Subjects.SystemPrivate, userId),
	}

	return permission, nil
}

func (s *NatsService) DeleteConsumer(roomId, userId string) {
	_ = s.js.DeleteConsumer(s.ctx, roomId, fmt.Sprintf("%s:%s", s.app.NatsInfo.Subjects.Chat, userId))
	_ = s.js.DeleteConsumer(s.ctx, roomId, fmt.Sprintf("%s:%s", s.app.NatsInfo.Subjects.SystemPublic, userId))
	_ = s.js.DeleteConsumer(s.ctx, roomId, fmt.Sprintf("%s:%s", s.app.NatsInfo.Subjects.SystemPrivate, userId))
}
