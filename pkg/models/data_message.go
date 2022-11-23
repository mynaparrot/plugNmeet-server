package models

import (
	"database/sql"
	"errors"
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

type DataMessageModel struct {
	db          *sql.DB
	roomService *RoomService
}

func NewDataMessageModel() *DataMessageModel {
	return &DataMessageModel{
		db:          config.AppCnf.DB,
		roomService: NewRoomService(),
	}
}

func (m *DataMessageModel) SendDataMessage(r *plugnmeet.DataMessageReq) error {
	switch r.MsgBodyType {
	case plugnmeet.DataMsgBodyType_RAISE_HAND:
		return m.raiseHand(r)
	case plugnmeet.DataMsgBodyType_LOWER_HAND:
		return m.lowerHand(r)
	case plugnmeet.DataMsgBodyType_OTHER_USER_LOWER_HAND:
		return m.otherUserLowerHand(r)
	case plugnmeet.DataMsgBodyType_INFO,
		plugnmeet.DataMsgBodyType_ALERT:
		return m.sendNotification(r)
	default:
		return errors.New(r.MsgBodyType.String() + " yet not ready")
	}
}

func (m *DataMessageModel) raiseHand(r *plugnmeet.DataMessageReq) error {
	participants, _ := m.roomService.LoadParticipants(r.RoomId)

	var sids []string
	for _, participant := range participants {
		meta := new(plugnmeet.UserMetadata)
		err := json.Unmarshal([]byte(participant.Metadata), meta)
		if err != nil {
			continue
		}
		if meta.IsAdmin && (r.RequestedUserId != participant.Identity) {
			sids = append(sids, participant.Sid)
		}
	}

	reqPar, _ := m.roomService.LoadParticipantInfo(r.RoomId, r.RequestedUserId)

	// now update user's metadata
	metadata := new(plugnmeet.UserMetadata)
	_ = json.Unmarshal([]byte(reqPar.Metadata), metadata)

	metadata.RaisedHand = true
	newMeta, err := json.Marshal(metadata)

	_, err = m.roomService.UpdateParticipantMetadata(r.RoomId, r.RequestedUserId, string(newMeta))
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
				Sid:    r.UserId,
				UserId: reqPar.Identity,
			},
			Msg: r.Msg,
		},
	}

	data, err := proto.Marshal(msg)
	if err != nil {
		log.Errorln(err)
		return err
	}

	// send as push message
	_, err = m.roomService.SendData(r.RoomId, data, livekit.DataPacket_RELIABLE, sids)
	if err != nil {
		log.Errorln(err)
		return err
	}

	return nil
}

func (m *DataMessageModel) lowerHand(r *plugnmeet.DataMessageReq) error {
	reqPar, err := m.roomService.LoadParticipantInfo(r.RoomId, r.RequestedUserId)
	if err != nil {
		return err
	}

	// now update user's metadata
	metadata := new(plugnmeet.UserMetadata)
	_ = json.Unmarshal([]byte(reqPar.Metadata), metadata)

	metadata.RaisedHand = false
	newMeta, err := json.Marshal(metadata)

	_, err = m.roomService.UpdateParticipantMetadata(r.RoomId, r.RequestedUserId, string(newMeta))
	if err != nil {
		return err
	}

	return nil
}

func (m *DataMessageModel) otherUserLowerHand(r *plugnmeet.DataMessageReq) error {
	if !r.IsAdmin {
		return errors.New("only allow for admin")
	}
	userId := r.Msg

	reqPar, err := m.roomService.LoadParticipantInfo(r.RoomId, userId)
	if err != nil {
		return err
	}

	// now update user's metadata
	metadata := new(plugnmeet.UserMetadata)
	_ = json.Unmarshal([]byte(reqPar.Metadata), metadata)

	metadata.RaisedHand = false
	newMeta, err := json.Marshal(metadata)

	_, err = m.roomService.UpdateParticipantMetadata(r.RoomId, userId, string(newMeta))
	if err != nil {
		return err
	}

	return nil
}

func (m *DataMessageModel) sendNotification(r *plugnmeet.DataMessageReq) error {
	mId := uuid.NewString()
	tm := time.Now().Format(time.RFC1123Z)

	msg := &plugnmeet.DataMessage{
		Type:      plugnmeet.DataMsgType_SYSTEM,
		MessageId: &mId,
		Body: &plugnmeet.DataMsgBody{
			Type: r.MsgBodyType,
			Time: &tm,
			From: &plugnmeet.DataMsgReqFrom{
				Sid: r.UserSid,
			},
			Msg: r.Msg,
		},
	}

	data, err := proto.Marshal(msg)
	if err != nil {
		return err
	}

	_, err = m.roomService.SendData(r.RoomId, data, livekit.DataPacket_RELIABLE, r.SendTo)
	if err != nil {
		log.Errorln(err)
		return err
	}

	return nil
}
