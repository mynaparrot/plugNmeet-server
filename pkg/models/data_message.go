package models

import (
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
	"time"
)

type DataMessageReq struct {
	Sid             string   `json:"sid"`
	RoomId          string   `json:"room_id"`
	MsgType         string   `json:"msg_type"`
	Msg             string   `json:"msg"`
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
	db             *sql.DB
	roomService    *RoomService
	analyticsModel *AnalyticsModel
}

func NewDataMessageModel() *DataMessageModel {
	return &DataMessageModel{
		db:             config.AppCnf.DB,
		roomService:    NewRoomService(),
		analyticsModel: NewAnalyticsModel(),
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
		plugnmeet.DataMsgBodyType_ALERT,
		plugnmeet.DataMsgBodyType_AZURE_COGNITIVE_SERVICE_SPEECH_TOKEN:
		return m.sendNotification(r)
	default:
		return errors.New(r.MsgBodyType.String() + " yet not ready")
	}
}

func (m *DataMessageModel) deliverMsg(roomId string, destinationUserIds []string, msg *plugnmeet.DataMessage) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		log.Errorln(err)
		return err
	}

	// send as push message
	_, err = m.roomService.SendData(roomId, data, livekit.DataPacket_RELIABLE, destinationUserIds)
	if err != nil {
		log.Errorln(err)
		return err
	}

	return nil
}

func (m *DataMessageModel) raiseHand(r *plugnmeet.DataMessageReq) error {
	participants, _ := m.roomService.LoadParticipants(r.RoomId)

	var ids []string
	for _, participant := range participants {
		meta, err := m.roomService.UnmarshalParticipantMetadata(participant.Metadata)
		if err != nil {
			continue
		}
		if meta.IsAdmin && (r.RequestedUserId != participant.Identity) {
			ids = append(ids, participant.Identity)
		}
	}

	reqPar, metadata, _ := m.roomService.LoadParticipantWithMetadata(r.RoomId, r.RequestedUserId)

	// now update user's metadata
	metadata.RaisedHand = true

	_, err := m.roomService.UpdateParticipantMetadataByStruct(r.RoomId, r.RequestedUserId, metadata)
	if err != nil {
		return err
	}

	if metadata.RaisedHand {
		m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
			EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_USER,
			EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_RAISE_HAND,
			RoomId:    r.RoomId,
			UserId:    &r.RequestedUserId,
		})
	}

	if len(ids) == 0 {
		return nil
	}

	mId := uuid.NewString()
	tm := time.Now().UTC().Format(time.RFC1123Z)
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

	// send as push message
	err = m.deliverMsg(r.RoomId, ids, msg)
	return err
}

func (m *DataMessageModel) lowerHand(r *plugnmeet.DataMessageReq) error {
	_, metadata, err := m.roomService.LoadParticipantWithMetadata(r.RoomId, r.RequestedUserId)
	if err != nil {
		return err
	}

	// now update user's metadata
	metadata.RaisedHand = false

	_, err = m.roomService.UpdateParticipantMetadataByStruct(r.RoomId, r.RequestedUserId, metadata)
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

	_, metadata, err := m.roomService.LoadParticipantWithMetadata(r.RoomId, userId)
	if err != nil {
		return err
	}

	// now update user's metadata
	metadata.RaisedHand = false

	_, err = m.roomService.UpdateParticipantMetadataByStruct(r.RoomId, userId, metadata)
	if err != nil {
		return err
	}

	return nil
}

func (m *DataMessageModel) sendNotification(r *plugnmeet.DataMessageReq) error {
	mId := uuid.NewString()
	tm := time.Now().UTC().Format(time.RFC1123Z)

	msg := &plugnmeet.DataMessage{
		Type:      plugnmeet.DataMsgType_SYSTEM,
		MessageId: &mId,
		Body: &plugnmeet.DataMsgBody{
			Type: r.GetMsgBodyType(),
			Time: &tm,
			From: &plugnmeet.DataMsgReqFrom{
				Sid: r.GetUserSid(),
			},
			Msg: r.GetMsg(),
		},
	}

	err := m.deliverMsg(r.GetRoomId(), r.GetSendTo(), msg)
	if err != nil {
		return err
	}

	return nil
}

func (m *DataMessageModel) SendUpdatedMetadata(roomId, metadata string) error {
	mId := uuid.NewString()
	tm := time.Now().UTC().Format(time.RFC1123Z)

	msg := &plugnmeet.DataMessage{
		Type:      plugnmeet.DataMsgType_SYSTEM,
		MessageId: &mId,
		Body: &plugnmeet.DataMsgBody{
			Type: plugnmeet.DataMsgBodyType_UPDATE_ROOM_METADATA,
			Time: &tm,
			From: &plugnmeet.DataMsgReqFrom{
				Sid: "system",
			},
			Msg: metadata,
		},
	}

	err := m.deliverMsg(roomId, []string{}, msg)
	return err
}
