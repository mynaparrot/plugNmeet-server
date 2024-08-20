package natsservice

import (
	"fmt"
	log "github.com/sirupsen/logrus"
)

func (s *NatsService) DeleteConsumer(roomId, userId string) {
	if err := s.js.DeleteConsumer(s.ctx, roomId, fmt.Sprintf("%s:%s", s.app.NatsInfo.Subjects.ChatPublic, userId)); err != nil {
		log.Errorln(err)
	}

	if err := s.js.DeleteConsumer(s.ctx, roomId, fmt.Sprintf("%s:%s", s.app.NatsInfo.Subjects.ChatPrivate, userId)); err != nil {
		log.Errorln(err)
	}

	if err := s.js.DeleteConsumer(s.ctx, roomId, fmt.Sprintf("%s:%s", s.app.NatsInfo.Subjects.SystemPublic, userId)); err != nil {
		log.Errorln(err)
	}

	if err := s.js.DeleteConsumer(s.ctx, roomId, fmt.Sprintf("%s:%s", s.app.NatsInfo.Subjects.SystemPrivate, userId)); err != nil {
		log.Errorln(err)
	}

	if err := s.js.DeleteConsumer(s.ctx, roomId, fmt.Sprintf("%s:%s", s.app.NatsInfo.Subjects.Whiteboard, userId)); err != nil {
		log.Errorln(err)
	}

	if err := s.js.DeleteConsumer(s.ctx, roomId, fmt.Sprintf("%s:%s", s.app.NatsInfo.Subjects.DataChannel, userId)); err != nil {
		log.Errorln(err)
	}
}
