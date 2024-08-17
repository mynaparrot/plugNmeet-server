package websocketmodel

import (
	"github.com/gofiber/contrib/socketio"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/authmodel"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
	"sync"
)

func (m *WebsocketModel) handleSystemMessages(payload *plugnmeet.DataMessage, roomId string) {
	switch payload.Body.Type {
	case plugnmeet.DataMsgBodyType_SEND_CHAT_MSGS,
		plugnmeet.DataMsgBodyType_INIT_WHITEBOARD:
		m.handleSendChatMsgs(payload, roomId) // we can use same method for both
	case plugnmeet.DataMsgBodyType_RENEW_TOKEN:
		m.handleRenewToken(payload, roomId)
	case plugnmeet.DataMsgBodyType_INFO, plugnmeet.DataMsgBodyType_ALERT:
		m.handleSendPushMsg(payload, roomId)
	case plugnmeet.DataMsgBodyType_USER_VISIBILITY_CHANGE:
		m.handleUserVisibility(payload, roomId)
	case plugnmeet.DataMsgBodyType_EXTERNAL_MEDIA_PLAYER_EVENTS:
		m.handleExternalMediaPlayerEvents(payload, roomId)
	case plugnmeet.DataMsgBodyType_POLL_CREATED,
		plugnmeet.DataMsgBodyType_NEW_POLL_RESPONSE,
		plugnmeet.DataMsgBodyType_POLL_CLOSED:
		m.handlePollsNotifications(payload, roomId)
	case plugnmeet.DataMsgBodyType_JOIN_BREAKOUT_ROOM:
		m.handleSendBreakoutRoomNotification(payload, roomId)
	case plugnmeet.DataMsgBodyType_SPEECH_SUBTITLE_TEXT:
		m.handleSpeechSubtitleText(payload, roomId)

	}
}

func (m *WebsocketModel) handleSendChatMsgs(pl *plugnmeet.DataMessage, roomId string) {
	jm, err := proto.Marshal(pl)
	if err != nil {
		return
	}
	var userUUID string

	m.app.RLock()
	for _, p := range m.app.GetChatParticipants(roomId) {
		if p.RoomSid == pl.RoomSid {
			if *pl.To == p.UserSid || *pl.To == p.UserId {
				userUUID = p.UUID
				break
			}
		}
	}
	m.app.RUnlock()

	if userUUID != "" {
		err = socketio.EmitTo(userUUID, jm, socketio.BinaryMessage)
		if err != nil {
			log.Errorln(err)
		}
	}
}

func (m *WebsocketModel) handleRenewToken(pl *plugnmeet.DataMessage, roomId string) {
	rm := authmodel.New(m.app, nil)
	token, err := rm.RenewPNMToken(pl.Body.Msg)
	if err != nil {
		return
	}

	payload := &plugnmeet.DataMessage{
		Type:      plugnmeet.DataMsgType_SYSTEM,
		MessageId: pl.MessageId,
		Body: &plugnmeet.DataMsgBody{
			Type: plugnmeet.DataMsgBodyType_RENEW_TOKEN,
			From: &plugnmeet.DataMsgReqFrom{
				Sid: "SYSTEM",
			},
			Msg: token,
		},
	}

	jm, err := proto.Marshal(payload)
	if err != nil {
		return
	}

	m.app.RLock()
	for _, p := range m.app.GetChatParticipants(roomId) {
		if p.RoomId == roomId && pl.Body.From.UserId == p.UserId {
			err = socketio.EmitTo(p.UUID, jm, socketio.BinaryMessage)
			if err != nil {
				log.Errorln(err)
			}
		}
	}
	m.app.RUnlock()
}

func (m *WebsocketModel) handleSendPushMsg(pl *plugnmeet.DataMessage, roomId string) {
	jm, err := proto.Marshal(pl)
	if err != nil {
		return
	}
	var to []string

	m.app.RLock()
	for _, p := range m.app.GetChatParticipants(roomId) {
		if p.RoomSid == pl.RoomSid {
			// only for specific user
			if pl.To != nil {
				if *pl.To == p.UserSid || *pl.To == p.UserId {
					to = append(to, p.UUID)
				}
			} else {
				// for everyone in the room
				to = append(to, p.UUID)
			}
		}
	}
	m.app.RUnlock()
	if len(to) > 0 {
		socketio.EmitToList(to, jm, socketio.BinaryMessage)
	}
}

func (m *WebsocketModel) handleUserVisibility(pl *plugnmeet.DataMessage, roomId string) {
	jm, err := proto.Marshal(pl)
	if err != nil {
		return
	}

	var to []string

	m.app.RLock()
	for _, p := range m.app.GetChatParticipants(roomId) {
		if p.RoomSid == pl.RoomSid {
			if pl.Body.From.UserId != p.UserId && p.IsAdmin {
				// we don't need to send update to sender
				to = append(to, p.UUID)
			}
		}
	}
	m.app.RUnlock()

	if len(to) > 0 {
		socketio.EmitToList(to, jm, socketio.BinaryMessage)
	}
}

func (m *WebsocketModel) handleExternalMediaPlayerEvents(pl *plugnmeet.DataMessage, roomId string) {
	jm, err := proto.Marshal(pl)
	if err != nil {
		return
	}

	var to []string

	m.app.RLock()
	for _, p := range m.app.GetChatParticipants(roomId) {
		if p.RoomSid == pl.RoomSid {
			if pl.Body.From.UserId != p.UserId {
				// we don't need to send update to sender
				to = append(to, p.UUID)
			}
		}
	}
	m.app.RUnlock()

	if len(to) > 0 {
		socketio.EmitToList(to, jm, socketio.BinaryMessage)
	}
}

func (m *WebsocketModel) handlePollsNotifications(pl *plugnmeet.DataMessage, roomId string) {
	jm, err := proto.Marshal(pl)
	if err != nil {
		return
	}

	var to []string

	m.app.RLock()
	for _, p := range m.app.GetChatParticipants(roomId) {
		if p.RoomId == roomId {
			if pl.Body.From.UserId != p.UserId {
				// we don't need to send update to sender
				to = append(to, p.UUID)
			}
		}
	}
	m.app.RUnlock()

	if len(to) > 0 {
		socketio.EmitToList(to, jm, socketio.BinaryMessage)
	}
}

func (m *WebsocketModel) handleSendBreakoutRoomNotification(pl *plugnmeet.DataMessage, roomId string) {
	jm, err := proto.Marshal(pl)
	if err != nil {
		return
	}
	var to []string

	m.app.RLock()
	for _, p := range m.app.GetChatParticipants(roomId) {
		if p.RoomId == roomId {
			// only for specific user
			if pl.To != nil {
				if *pl.To == p.UserId {
					to = append(to, p.UUID)
				}
			} else {
				// for everyone in the room
				to = append(to, p.UUID)
			}
		}
	}
	m.app.RUnlock()
	if len(to) > 0 {
		socketio.EmitToList(to, jm, socketio.BinaryMessage)
	}
}

func (m *WebsocketModel) handleSpeechSubtitleText(pl *plugnmeet.DataMessage, roomId string) {
	jm, err := proto.Marshal(pl)
	if err != nil {
		return
	}

	var to []string

	m.app.RLock()
	for _, p := range m.app.GetChatParticipants(roomId) {
		if p.RoomSid == pl.RoomSid {
			if pl.To != nil {
				if *pl.To == p.UserSid || *pl.To == p.UserId {
					to = append(to, p.UUID)
				}
			} else {
				// for everyone in the room
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
