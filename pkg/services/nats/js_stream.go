package natsservice

import (
	"fmt"

	"github.com/nats-io/nats.go/jetstream"
)

const PnmRoomStream = "pnm-room-stream"

// CreateRoomNatsStreams will create a single stream for all rooms.
func (s *NatsService) CreateRoomNatsStreams() error {
	_, err := s.js.CreateOrUpdateStream(s.ctx, jetstream.StreamConfig{
		Name:     PnmRoomStream,
		Replicas: s.app.NatsInfo.NumReplicas,
		Subjects: []string{
			fmt.Sprintf("%s.>", s.app.NatsInfo.Subjects.Chat),
			fmt.Sprintf("%s.>", s.app.NatsInfo.Subjects.SystemPublic),
			fmt.Sprintf("%s.>", s.app.NatsInfo.Subjects.SystemPrivate),
		},
	})
	if err != nil {
		return err
	}

	return nil
}

func (s *NatsService) DeleteRoomNatsStream(roomId string) error {
	// Purge all subjects for the specific room under the new hierarchy.
	stream, err := s.js.Stream(s.ctx, PnmRoomStream)
	if err != nil {
		return err
	}
	return stream.Purge(s.ctx,
		jetstream.WithPurgeSubject(fmt.Sprintf("%s.%s.>", s.app.NatsInfo.Subjects.Chat, roomId)),
		jetstream.WithPurgeSubject(fmt.Sprintf("%s.%s.>", s.app.NatsInfo.Subjects.SystemPublic, roomId)),
		jetstream.WithPurgeSubject(fmt.Sprintf("%s.%s.>", s.app.NatsInfo.Subjects.SystemPrivate, roomId)),
	)
}
