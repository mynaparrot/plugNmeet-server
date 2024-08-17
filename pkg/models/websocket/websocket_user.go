package websocketmodel

import (
	"github.com/gofiber/contrib/socketio"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
	"sync"
)

func (m *WebsocketModel) userMessages(payload *plugnmeet.DataMessage, roomId string) {
	switch payload.Body.Type {
	case plugnmeet.DataMsgBodyType_CHAT:
		m.handleChat(payload, roomId)
	}
}

func (m *WebsocketModel) handleChat(payload *plugnmeet.DataMessage, roomId string) {
	jm, err := proto.Marshal(payload)
	if err != nil {
		return
	}

	var to []string

	config.GetConfig().RLock()
	for _, p := range config.GetConfig().GetChatParticipants(roomId) {
		if p.RoomId == roomId {
			// only for specific user
			if payload.To != nil {
				if *payload.To == p.UserId {
					to = append(to, p.UUID)
				}
				// for private messages we should send this message back to sender as well as
				if payload.Body.IsPrivate != nil && *payload.Body.IsPrivate == 1 {
					if payload.Body.From.UserId == p.UserId {
						to = append(to, p.UUID)
					}
				}
			} else {
				// for everyone in the room
				to = append(to, p.UUID)
			}
		}
	}
	config.GetConfig().RUnlock()

	l := len(to)
	if l > 0 {
		var wg sync.WaitGroup
		// for network related issue delivery can be delay
		// if this continues then messages will be overflow & drop
		// using concurrent will give a better result
		// if one user have bad connection then waiting only for him
		wg.Add(l)
		for _, t := range to {
			go func(u string) {
				defer wg.Done()
				err := socketio.EmitTo(u, jm, socketio.BinaryMessage)
				if err != nil {
					log.Errorln(err)
				}
			}(t)
		}
		wg.Wait()
	}
}
