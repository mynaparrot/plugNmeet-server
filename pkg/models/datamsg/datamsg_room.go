package datamsgmodel

import (
	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"time"
)

func (m *DataMsgModel) sendNotification(r *plugnmeet.DataMessageReq) error {
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

func (m *DataMsgModel) SendUpdatedMetadata(roomId, metadata string) error {
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
