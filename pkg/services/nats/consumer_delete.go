package natsservice

import (
	"fmt"
	log "github.com/sirupsen/logrus"
)

func (s *NatsService) DeleteConsumer(roomId, userId string) {
	err := s.js.DeleteConsumer(s.ctx, roomId, fmt.Sprintf("%s:%s", s.app.NatsInfo.Subjects.ChatPublic, userId))
	if err != nil {
		log.Errorln(err)
	}
	err = s.js.DeleteConsumer(s.ctx, roomId, fmt.Sprintf("%s:%s", s.app.NatsInfo.Subjects.ChatPrivate, userId))
	if err != nil {
		log.Errorln(err)
	}
	err = s.js.DeleteConsumer(s.ctx, roomId, fmt.Sprintf("%s:%s", s.app.NatsInfo.Subjects.SystemPublic, userId))
	if err != nil {
		log.Errorln(err)
	}
	err = s.js.DeleteConsumer(s.ctx, roomId, fmt.Sprintf("%s:%s", s.app.NatsInfo.Subjects.SystemPrivate, userId))
	if err != nil {
		log.Errorln(err)
	}
	err = s.js.DeleteConsumer(s.ctx, roomId, fmt.Sprintf("%s:%s", s.app.NatsInfo.Subjects.Whiteboard, userId))
	if err != nil {
		log.Errorln(err)
	}
}
