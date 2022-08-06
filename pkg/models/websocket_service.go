package models

import (
	"fmt"
	"github.com/antoniodipinto/ikisocket"
	"github.com/goccy/go-json"
	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"google.golang.org/protobuf/proto"
	"time"
)

type websocketService struct {
	pl      *plugnmeet.DataMessage // payload msg
	rSid    string                 // room sid
	isAdmin bool
	roomId  string
}

func NewWebsocketService() *websocketService {
	return &websocketService{}
}

func (w *websocketService) HandleDataMessages(payload *plugnmeet.DataMessage, roomId string, isAdmin bool) {
	if payload.MessageId == nil {
		uu := uuid.NewString()
		payload.MessageId = &uu
	}
	if payload.Body.Time == nil {
		tt := time.Now().Format(time.RFC1123Z)
		payload.Body.Time = &tt
	}
	w.pl = payload           // payload messages
	w.rSid = payload.RoomSid // room sid
	w.isAdmin = isAdmin
	w.roomId = roomId

	switch payload.Type {
	case plugnmeet.DataMsgType_USER:
		w.userMessages()
	case plugnmeet.DataMsgType_SYSTEM:
		w.handleSystemMessages()
	case plugnmeet.DataMsgType_WHITEBOARD:
		w.handleWhiteboardMessages()
	}
}

func (w *websocketService) userMessages() {
	switch w.pl.Body.Type {
	case plugnmeet.DataMsgBodyType_CHAT:
		w.handleChat()
	}
}

func (w *websocketService) handleSystemMessages() {
	switch w.pl.Body.Type {
	case plugnmeet.DataMsgBodyType_SEND_CHAT_MSGS,
		plugnmeet.DataMsgBodyType_INIT_WHITEBOARD:
		w.handleSendChatMsgs() // we can use same method for both
	case plugnmeet.DataMsgBodyType_RENEW_TOKEN:
		w.handleRenewToken()
	case plugnmeet.DataMsgBodyType_INFO, plugnmeet.DataMsgBodyType_ALERT:
		w.handleSendPushMsg()
	case plugnmeet.DataMsgBodyType_USER_VISIBILITY_CHANGE:
		w.handleUserVisibility()
	case plugnmeet.DataMsgBodyType_EXTERNAL_MEDIA_PLAYER_EVENTS:
		w.handleExternalMediaPlayerEvents()
	case plugnmeet.DataMsgBodyType_POLL_CREATED,
		plugnmeet.DataMsgBodyType_NEW_POLL_RESPONSE,
		plugnmeet.DataMsgBodyType_POLL_CLOSED:
		w.handlePollsNotifications()
	case plugnmeet.DataMsgBodyType_JOIN_BREAKOUT_ROOM:
		w.handleSendBreakoutRoomNotification()
	}
}

func (w *websocketService) handleWhiteboardMessages() {
	switch w.pl.Body.Type {
	case plugnmeet.DataMsgBodyType_SCENE_UPDATE,
		plugnmeet.DataMsgBodyType_POINTER_UPDATE,
		plugnmeet.DataMsgBodyType_ADD_WHITEBOARD_FILE,
		plugnmeet.DataMsgBodyType_ADD_WHITEBOARD_OFFICE_FILE,
		plugnmeet.DataMsgBodyType_PAGE_CHANGE:
		w.handleWhiteboard()
	}
}

func (w *websocketService) handleChat() {
	jm, err := json.Marshal(w.pl)
	if err != nil {
		return
	}

	var to []string

	config.AppCnf.RLock()
	for _, p := range config.AppCnf.GetChatParticipants(w.roomId) {
		if p.RoomId == w.roomId {
			// only for specific user
			if w.pl.To != nil {
				if w.pl.To == &p.UserId {
					to = append(to, p.UUID)
				}
				// for private messages we should send this message back to sender as well as
				if w.pl.Body.IsPrivate {
					if w.pl.Body.From.UserId == p.UserId {
						to = append(to, p.UUID)
					}
				}
			} else {
				// for everyone in the room
				to = append(to, p.UUID)
			}
		}
	}
	config.AppCnf.RUnlock()

	if len(to) > 0 {
		ikisocket.EmitToList(to, jm)
	}
}

func (w *websocketService) handleSendChatMsgs() {
	jm, err := json.Marshal(w.pl)
	if err != nil {
		return
	}
	var userUUID string

	config.AppCnf.RLock()
	for _, p := range config.AppCnf.GetChatParticipants(w.roomId) {
		if p.RoomSid == w.rSid {
			if w.pl.To == &p.UserSid {
				userUUID = p.UUID
				break
			}
		}
	}
	config.AppCnf.RUnlock()

	if userUUID != "" {
		err = ikisocket.EmitTo(userUUID, jm)
		if err != nil {
			fmt.Println(err)
		}
	}
}

func (w *websocketService) handleRenewToken() {
	req := &ValidateTokenReq{
		Token:  w.pl.Body.Msg,
		RoomId: w.pl.RoomId,
		Sid:    w.pl.RoomSid,
	}
	m := NewAuthTokenModel()
	token, err := m.DoRenewToken(req)
	if err != nil {
		return
	}

	payload := &plugnmeet.DataMessage{
		Type:      plugnmeet.DataMsgType_SYSTEM,
		MessageId: w.pl.MessageId,
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

	config.AppCnf.RLock()
	for _, p := range config.AppCnf.GetChatParticipants(w.roomId) {
		if p.RoomSid == w.rSid {
			if w.pl.Body.From.UserId == p.UserId {
				err = ikisocket.EmitTo(p.UUID, jm)
				if err != nil {
					fmt.Println(err)
				}
			}
		}
	}
	config.AppCnf.RUnlock()
}

func (w *websocketService) handleSendPushMsg() {
	jm, err := json.Marshal(w.pl)
	if err != nil {
		return
	}
	var to []string

	config.AppCnf.RLock()
	for _, p := range config.AppCnf.GetChatParticipants(w.roomId) {
		if p.RoomSid == w.rSid {
			// only for specific user
			if w.pl.To != nil {
				if w.pl.To == &p.UserSid {
					to = append(to, p.UUID)
				}
			} else {
				// for everyone in the room
				to = append(to, p.UUID)
			}
		}
	}
	config.AppCnf.RUnlock()
	if len(to) > 0 {
		ikisocket.EmitToList(to, jm)
	}
}

func (w *websocketService) handleWhiteboard() {
	jm, err := json.Marshal(w.pl)
	if err != nil {
		return
	}

	var to []string

	config.AppCnf.RLock()
	for _, p := range config.AppCnf.GetChatParticipants(w.roomId) {
		if p.RoomSid == w.rSid {
			// this is basically for initial request
			if w.pl.To != nil {
				if w.pl.To == &p.UserSid {
					to = append(to, p.UUID)
				}
			} else if w.pl.Body.From.UserId != p.UserId {
				// we don't need to send update to sender
				to = append(to, p.UUID)
			}
		}
	}
	config.AppCnf.RUnlock()

	if len(to) > 0 {
		ikisocket.EmitToList(to, jm)
	}
}

func (w *websocketService) handleUserVisibility() {
	jm, err := json.Marshal(w.pl)
	if err != nil {
		return
	}

	var to []string

	config.AppCnf.RLock()
	for _, p := range config.AppCnf.GetChatParticipants(w.roomId) {
		if p.RoomSid == w.rSid {
			if w.pl.Body.From.UserId != p.UserId && p.IsAdmin {
				// we don't need to send update to sender
				to = append(to, p.UUID)
			}
		}
	}
	config.AppCnf.RUnlock()

	if len(to) > 0 {
		ikisocket.EmitToList(to, jm)
	}
}

func (w *websocketService) handleExternalMediaPlayerEvents() {
	jm, err := json.Marshal(w.pl)
	if err != nil {
		return
	}

	var to []string

	config.AppCnf.RLock()
	for _, p := range config.AppCnf.GetChatParticipants(w.roomId) {
		if p.RoomSid == w.rSid {
			if w.pl.Body.From.UserId != p.UserId {
				// we don't need to send update to sender
				to = append(to, p.UUID)
			}
		}
	}
	config.AppCnf.RUnlock()

	if len(to) > 0 {
		ikisocket.EmitToList(to, jm)
	}
}

func (w *websocketService) handlePollsNotifications() {
	jm, err := json.Marshal(w.pl)
	if err != nil {
		return
	}

	var to []string

	config.AppCnf.RLock()
	for _, p := range config.AppCnf.GetChatParticipants(w.roomId) {
		if p.RoomId == w.roomId {
			if w.pl.Body.From.UserId != p.UserId {
				// we don't need to send update to sender
				to = append(to, p.UUID)
			}
		}
	}
	config.AppCnf.RUnlock()

	if len(to) > 0 {
		ikisocket.EmitToList(to, jm)
	}
}

func (w *websocketService) handleSendBreakoutRoomNotification() {
	jm, err := json.Marshal(w.pl)
	if err != nil {
		return
	}
	var to []string

	config.AppCnf.RLock()
	for _, p := range config.AppCnf.GetChatParticipants(w.roomId) {
		if p.RoomId == w.roomId {
			// only for specific user
			if w.pl.To != nil {
				if w.pl.To == &p.UserId {
					to = append(to, p.UUID)
				}
			} else {
				// for everyone in the room
				to = append(to, p.UUID)
			}
		}
	}
	config.AppCnf.RUnlock()
	if len(to) > 0 {
		ikisocket.EmitToList(to, jm)
	}
}
