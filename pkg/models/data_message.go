package models

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/goccy/go-json"
	"github.com/google/uuid"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
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
	msgBodyType     plugnmeet.DataMsgBodyType
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
		r.msgBodyType = plugnmeet.DataMsgBodyType_RAISE_HAND
		return r.RaiseHand()
	case "LOWER_HAND":
		r.msgBodyType = plugnmeet.DataMsgBodyType_LOWER_HAND
		return r.LowerHand()
	case "OTHER_USER_LOWER_HAND":
		r.msgBodyType = plugnmeet.DataMsgBodyType_OTHER_USER_LOWER_HAND
		return r.OtherUserLowerHand()
	case "INFO":
		r.msgBodyType = plugnmeet.DataMsgBodyType_INFO
		return r.SendNotification(r.SendTo)
	case "ALERT":
		r.msgBodyType = plugnmeet.DataMsgBodyType_ALERT
		return r.SendNotification(r.SendTo)

	default:
		return errors.New(r.MsgType + " yet not ready")
	}
}

func (d *DataMessageReq) RaiseHand() error {
	participants, _ := d.roomService.LoadParticipantsFromRedis(d.RoomId)

	var sids []string
	for _, participant := range participants {
		meta := new(plugnmeet.UserMetadata)
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
	metadata := new(plugnmeet.UserMetadata)
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

	mId := uuid.NewString()
	tm := time.Now().Format(time.RFC1123Z)
	msg := &plugnmeet.DataMessage{
		Type:      plugnmeet.DataMsgType_SYSTEM,
		MessageId: &mId,
		Body: &plugnmeet.DataMsgBody{
			Type: plugnmeet.DataMsgBodyType_RAISE_HAND,
			Time: &tm,
			From: &plugnmeet.DataMsgReqFrom{
				Sid:    d.Sid,
				UserId: reqPar.Identity,
			},
			Msg: d.Msg,
		},
	}

	data, err := proto.Marshal(msg)
	if err != nil {
		log.Errorln(err)
		return err
	}

	// send as push message
	_, err = d.roomService.SendData(d.RoomId, data, livekit.DataPacket_RELIABLE, sids)
	if err != nil {
		log.Errorln(err)
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
	metadata := new(plugnmeet.UserMetadata)
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
	metadata := new(plugnmeet.UserMetadata)
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
	mId := uuid.NewString()
	tm := time.Now().Format(time.RFC1123Z)

	msg := &plugnmeet.DataMessage{
		Type:      plugnmeet.DataMsgType_SYSTEM,
		MessageId: &mId,
		Body: &plugnmeet.DataMsgBody{
			Type: d.msgBodyType,
			Time: &tm,
			From: &plugnmeet.DataMsgReqFrom{
				Sid: d.Sid,
			},
			Msg: d.Msg,
		},
	}

	data, err := proto.Marshal(msg)
	if err != nil {
		return err
	}

	_, err = d.roomService.SendData(d.RoomId, data, livekit.DataPacket_RELIABLE, sendTo)
	if err != nil {
		log.Errorln(err)
		return err
	}

	return nil
}
