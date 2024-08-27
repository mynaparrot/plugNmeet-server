package natsservice

import (
	"fmt"
)

func (s *NatsService) DeleteConsumer(roomId, userId string) {
	_ = s.js.DeleteConsumer(s.ctx, roomId, fmt.Sprintf("%s:%s", s.app.NatsInfo.Subjects.Chat, userId))
	_ = s.js.DeleteConsumer(s.ctx, roomId, fmt.Sprintf("%s:%s", s.app.NatsInfo.Subjects.SystemPublic, userId))
	_ = s.js.DeleteConsumer(s.ctx, roomId, fmt.Sprintf("%s:%s", s.app.NatsInfo.Subjects.SystemPrivate, userId))
	_ = s.js.DeleteConsumer(s.ctx, roomId, fmt.Sprintf("%s:%s", s.app.NatsInfo.Subjects.Whiteboard, userId))
	_ = s.js.DeleteConsumer(s.ctx, roomId, fmt.Sprintf("%s:%s", s.app.NatsInfo.Subjects.DataChannel, userId))
}
