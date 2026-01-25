package natsservice

import (
	"fmt"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/sirupsen/logrus"
)

const DurableNameTpl = "%s_%s"

// CreateRoomNatsStream will create a single stream for all rooms.
func (s *NatsService) CreateRoomNatsStream(logger *logrus.Entry) error {
	_, err := s.js.CreateOrUpdateStream(s.ctx, jetstream.StreamConfig{
		Name:     s.app.NatsInfo.RoomStreamName,
		Replicas: s.app.NatsInfo.NumReplicas,
		Subjects: []string{
			fmt.Sprintf("%s.>", s.app.NatsInfo.Subjects.SystemPublic),
			fmt.Sprintf("%s.>", s.app.NatsInfo.Subjects.SystemPrivate),
		},
	})
	if err != nil {
		return err
	}
	logger.Infof("successfully created room stream: %s", s.app.NatsInfo.RoomStreamName)

	return nil
}

func (s *NatsService) DeleteRoomNatsStream(roomId string) error {
	// Purge all subjects for the specific room.
	stream, err := s.js.Stream(s.ctx, s.app.NatsInfo.RoomStreamName)
	if err != nil {
		return err
	}
	return stream.Purge(s.ctx,
		jetstream.WithPurgeSubject(fmt.Sprintf("%s.%s.>", s.app.NatsInfo.Subjects.SystemPublic, roomId)),
		jetstream.WithPurgeSubject(fmt.Sprintf("%s.%s.>", s.app.NatsInfo.Subjects.SystemPrivate, roomId)),
	)
}

// CreateUserConsumer creates a single consumer per user for chat, public, and private system messages.
func (s *NatsService) CreateUserConsumer(roomId, userId string) (stream string, durableName string, err error) {
	durableName = fmt.Sprintf(DurableNameTpl, roomId, userId)
	_, err = s.js.CreateOrUpdateConsumer(s.ctx, s.app.NatsInfo.RoomStreamName, jetstream.ConsumerConfig{
		Durable:       durableName,
		DeliverPolicy: jetstream.DeliverNewPolicy,
		FilterSubjects: []string{
			// e.g., "sysPublic.room123.>"
			fmt.Sprintf("%s.%s.>", s.app.NatsInfo.Subjects.SystemPublic, roomId),
			// e.g., "sysPrivate.room123.user456.>"
			fmt.Sprintf("%s.%s.%s.>", s.app.NatsInfo.Subjects.SystemPrivate, roomId, userId),
		},
	})
	if err != nil {
		return "", "", err
	}

	return s.app.NatsInfo.RoomStreamName, durableName, nil
}

func (s *NatsService) DeleteConsumer(roomId, userId string) {
	durableName := fmt.Sprintf(DurableNameTpl, roomId, userId)
	_ = s.js.DeleteConsumer(s.ctx, s.app.NatsInfo.RoomStreamName, durableName)
}
