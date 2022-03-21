package models

import (
	"encoding/json"
	"fmt"
	"github.com/antoniodipinto/ikisocket"
	"github.com/google/uuid"
	"github.com/mynaparrot/plugNmeet/internal/config"
	"time"
)

type WebsocketRedisMsg struct {
	Type    string
	Payload *DataMessageRes
	RoomId  *string
	IsAdmin bool
}

type websocketService struct {
	pl           *DataMessageRes // payload msg
	rSid         *string         // room sid
	isAdmin      bool
	participants map[string]*config.ChatParticipant
}

func NewWebsocketService() *websocketService {
	return &websocketService{}
}

func (w *websocketService) HandleDataMessages(payload *DataMessageRes, roomId *string, isAdmin bool) {
	if payload.MessageId == "" {
		payload.MessageId = uuid.NewString()
	}
	if payload.Body.Time == "" {
		payload.Body.Time = time.Now().Format(time.RFC1123Z)
	}
	w.pl = payload            // payload messages
	w.rSid = &payload.RoomSid // room sid

	if _, ok := config.AppCnf.ChatRooms[*roomId]; ok {
		w.participants = config.AppCnf.ChatRooms[*roomId]
	}

	w.isAdmin = isAdmin

	switch payload.Type {
	case "USER":
		w.userMessages()
	case "SYSTEM":
		w.handleSystemMessages()
	case "WHITEBOARD":
		w.handleWhiteboardMessages()
	}
}

func (w *websocketService) userMessages() {
	switch w.pl.Body.Type {
	case "CHAT":
		w.handleChat()
	}
}

func (w *websocketService) handleSystemMessages() {
	switch w.pl.Body.Type {
	case "SEND_CHAT_MSGS",
		"INIT_WHITEBOARD":
		w.handleSendChatMsgs() // we can use same method for both
	case "RENEW_TOKEN":
		w.handleRenewToken()
	case "INFO", "ALERT":
		w.handleSendPushMsg()
	}
}

func (w *websocketService) handleWhiteboardMessages() {
	switch w.pl.Body.Type {
	case "SCENE_UPDATE",
		"POINTER_UPDATE",
		"ADD_WHITEBOARD_FILE":
		w.handleWhiteboard()
	}
}

func (w *websocketService) handleChat() {
	jm, err := json.Marshal(w.pl)
	if err != nil {
		return
	}

	var to []string
	if w.participants != nil {
		for _, p := range w.participants {
			if p.RoomSid == *w.rSid {
				// only for specific user
				if w.pl.To != "" {
					if w.pl.To == p.UserSid {
						to = append(to, p.UUID)
					}
				} else {
					// for everyone in the room
					to = append(to, p.UUID)
				}
			}
		}
	}

	if len(to) > 0 {
		ikisocket.EmitToList(to, jm)
	}
}

func (w *websocketService) handleSendChatMsgs() {
	jm, err := json.Marshal(w.pl)
	if err != nil {
		return
	}
	if w.participants != nil {
		for _, p := range w.participants {
			if p.RoomSid == *w.rSid {
				if w.pl.To == p.UserSid {
					err = ikisocket.EmitTo(p.UUID, jm)
					if err != nil {
						fmt.Println(err)
					}
				}
			}
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

	payload := DataMessageRes{
		Type:      "SYSTEM",
		MessageId: w.pl.MessageId,
		Body: DataMessageBody{
			Type: "RENEW_TOKEN",
			From: ReqFrom{
				Sid: "SYSTEM",
			},
			Msg: token,
		},
	}

	jm, err := json.Marshal(payload)
	if err != nil {
		return
	}

	if w.participants != nil {
		for _, p := range w.participants {
			if p.RoomSid == *w.rSid {
				if w.pl.Body.From.UserId == p.UserId {
					err = ikisocket.EmitTo(p.UUID, jm)
					if err != nil {
						fmt.Println(err)
					}
				}
			}
		}
	}
}

func (w *websocketService) handleSendPushMsg() {
	jm, err := json.Marshal(w.pl)
	if err != nil {
		return
	}
	var to []string

	if w.participants != nil {
		for _, p := range w.participants {
			if p.RoomSid == *w.rSid {
				// only for specific user
				if w.pl.To != "" {
					if w.pl.To == p.UserSid {
						to = append(to, p.UUID)
					}
				} else {
					// for everyone in the room
					to = append(to, p.UUID)
				}
			}
		}
	}

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
	if w.participants != nil {
		for _, p := range w.participants {
			if p.RoomSid == *w.rSid {
				// this is basically for initial request
				if w.pl.To != "" {
					if w.pl.To == p.UserSid {
						to = append(to, p.UUID)
					}
				} else if w.pl.Body.From.UserId != p.UserId {
					// we don't need to send update to sender
					to = append(to, p.UUID)
				}
			}
		}
	}

	if len(to) > 0 {
		ikisocket.EmitToList(to, jm)
	}
}
