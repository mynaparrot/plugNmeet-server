package websocketmodel

import (
	"github.com/gofiber/contrib/socketio"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"google.golang.org/protobuf/proto"
	"sync"
)

func (m *WebsocketModel) handleWhiteboardMessages(payload *plugnmeet.DataMessage, roomId string) {
	switch payload.Body.Type {
	case plugnmeet.DataMsgBodyType_SCENE_UPDATE,
		plugnmeet.DataMsgBodyType_POINTER_UPDATE,
		plugnmeet.DataMsgBodyType_ADD_WHITEBOARD_FILE,
		plugnmeet.DataMsgBodyType_ADD_WHITEBOARD_OFFICE_FILE,
		plugnmeet.DataMsgBodyType_PAGE_CHANGE,
		plugnmeet.DataMsgBodyType_WHITEBOARD_APP_STATE_CHANGE:
		m.handleWhiteboard(payload, roomId)
	}
}

func (m *WebsocketModel) handleWhiteboard(pl *plugnmeet.DataMessage, roomId string) {
	jm, err := proto.Marshal(pl)
	if err != nil {
		return
	}

	var to []string

	m.app.RLock()
	for _, p := range m.app.GetChatParticipants(roomId) {
		if p.RoomSid == pl.RoomSid {
			// this is basically for initial request
			if pl.To != nil {
				if *pl.To == p.UserSid || *pl.To == p.UserId {
					to = append(to, p.UUID)
				}
			} else if pl.Body.From.UserId != p.UserId {
				// we don't need to send update to sender
				to = append(to, p.UUID)
			}
		}
	}
	m.app.RUnlock()

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
				_ = socketio.EmitTo(u, jm, socketio.BinaryMessage)
			}(t)
		}
		wg.Wait()
	}
}
