package models

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugNmeet/internal/config"
	"time"
)

type DataMessageReq struct {
	Sid             string   `json:"sid" validate:"required"`
	RoomId          string   `json:"room_id" validate:"required"`
	MsgType         string   `json:"msg_type" validate:"required"`
	Msg             string   `json:"msg" validate:"required"`
	RequestedUserId string   `json:"-"`
	SendTo          []string // user sids
	IsAdmin         bool
	db              *sql.DB
	roomService     *RoomService
}

type DataMessageRes struct {
	Type      string          `json:"type"` // 'SYSTEM' | 'USER' | 'WHITEBOARD'
	MessageId string          `json:"message_id"`
	RoomSid   string          `json:"room_sid"`
	RoomId    string          `json:"room_id"`
	To        string          `json:"to"`
	Body      DataMessageBody `json:"body"`
}

type DataMessageBody struct {
	Type      string  `json:"type"` // RAISE_HAND, LOWER_HAND, FILE_UPLOAD, INFO, ALERT, SEND_CHAT_MSGS, RENEW_TOKEN, INIT_WHITEBOARD, SCENE_UPDATE, POINTER_UPDATE
	MessageId string  `json:"message_id"`
	Time      string  `json:"time"`
	From      ReqFrom `json:"from"`
	Msg       string  `json:"msg"`
	IsPrivate bool    `json:"isPrivate"`
}

type ReqFrom struct {
	Sid    string `json:"sid"`
	UserId string `json:"userId"`
	Name   string `json:"name"`
}

func NewDataMessage(r *DataMessageReq) error {
	r.db = config.AppCnf.DB
	r.roomService = NewRoomService()

	switch r.MsgType {
	case "RAISE_HAND":
		return r.RaiseHand()
	case "LOWER_HAND":
		return r.LowerHand()
	case "OTHER_USER_LOWER_HAND":
		return r.OtherUserLowerHand()
	case "INFO", "ALERT":
		return r.SendNotification(r.SendTo)

	default:
		return errors.New(r.MsgType + " yet not ready")
	}
}

func (d *DataMessageReq) RaiseHand() error {
	participants, _ := d.roomService.LoadParticipantsFromRedis(d.RoomId)

	var sids []string
	for _, participant := range participants {
		meta := new(UserMetadata)
		err := json.Unmarshal([]byte(participant.Metadata), meta)
		if err != nil {
			continue
		}
		if meta.IsAdmin && (d.RequestedUserId != participant.Identity) {
			sids = append(sids, participant.Sid)
		}
	}

	reqPar, _ := d.roomService.LoadParticipantInfoFromRedis(d.RoomId, d.RequestedUserId)

	// now update user's metadata
	metadata := new(UserMetadata)
	_ = json.Unmarshal([]byte(reqPar.Metadata), metadata)

	metadata.RaisedHand = true
	newMeta, err := json.Marshal(metadata)

	_, err = d.roomService.UpdateParticipantMetadata(d.RoomId, d.RequestedUserId, string(newMeta))
	if err != nil {
		return err
	}

	if len(sids) == 0 {
		return nil
	}

	msg := DataMessageRes{
		Type:      "SYSTEM",
		MessageId: uuid.NewString(),
		Body: DataMessageBody{
			Type: "RAISE_HAND",
			Time: time.Now().Format(time.RFC1123Z),
			From: ReqFrom{
				Sid:    d.Sid,
				UserId: reqPar.Identity,
			},
			Msg: d.Msg,
		},
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	// send as push message
	_, err = d.roomService.SendData(d.RoomId, data, livekit.DataPacket_RELIABLE, sids)
	if err != nil {
		return err
	}

	return nil
}

func (d *DataMessageReq) LowerHand() error {
	reqPar, err := d.roomService.LoadParticipantInfoFromRedis(d.RoomId, d.RequestedUserId)
	if err != nil {
		return err
	}

	// now update user's metadata
	metadata := new(UserMetadata)
	_ = json.Unmarshal([]byte(reqPar.Metadata), metadata)

	metadata.RaisedHand = false
	newMeta, err := json.Marshal(metadata)

	_, err = d.roomService.UpdateParticipantMetadata(d.RoomId, d.RequestedUserId, string(newMeta))
	if err != nil {
		return err
	}

	return nil
}

func (d *DataMessageReq) OtherUserLowerHand() error {
	fmt.Println(d.IsAdmin)
	if !d.IsAdmin {
		return errors.New("only allow for admin")
	}
	userId := d.Msg

	reqPar, err := d.roomService.LoadParticipantInfoFromRedis(d.RoomId, userId)
	if err != nil {
		return err
	}

	// now update user's metadata
	metadata := new(UserMetadata)
	_ = json.Unmarshal([]byte(reqPar.Metadata), metadata)

	metadata.RaisedHand = false
	newMeta, err := json.Marshal(metadata)

	_, err = d.roomService.UpdateParticipantMetadata(d.RoomId, userId, string(newMeta))
	if err != nil {
		return err
	}

	return nil
}

func (d *DataMessageReq) SendNotification(sendTo []string) error {
	msg := DataMessageRes{
		Type:      "SYSTEM",
		MessageId: uuid.NewString(),
		Body: DataMessageBody{
			Type: d.MsgType,
			Time: time.Now().Format(time.RFC1123Z),
			From: ReqFrom{
				Sid: "system",
			},
			Msg: d.Msg,
		},
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	_, err = d.roomService.SendData(d.RoomId, data, livekit.DataPacket_RELIABLE, sendTo)
	if err != nil {
		return err
	}

	return nil
}
