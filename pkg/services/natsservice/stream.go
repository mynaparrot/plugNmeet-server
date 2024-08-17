package natsservice

import (
	"fmt"
	"github.com/nats-io/nats.go/jetstream"
)

func (s *NatsService) CreateRoomNatsStreams(roomId string) error {
	_, err := s.js.CreateOrUpdateStream(s.ctx, jetstream.StreamConfig{
		Name: roomId,
		Subjects: []string{
			fmt.Sprintf("%s:%s.*", roomId, s.app.NatsInfo.Subjects.ChatPublic),
			fmt.Sprintf("%s:%s.*.*", roomId, s.app.NatsInfo.Subjects.ChatPrivate),
			fmt.Sprintf("%s:%s.*", roomId, s.app.NatsInfo.Subjects.SystemPublic),
			fmt.Sprintf("%s:%s.*.*", roomId, s.app.NatsInfo.Subjects.SystemPrivate),
			fmt.Sprintf("%s:%s.*", roomId, s.app.NatsInfo.Subjects.Whiteboard),
		},
	})
	if err != nil {
		return err
	}

	return nil
}

func (s *NatsService) DeleteRoomNatsStream(roomId string) error {
	return s.js.DeleteStream(s.ctx, roomId)
}
