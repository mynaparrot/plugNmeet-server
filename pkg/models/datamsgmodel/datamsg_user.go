package datamsgmodel

import (
	"errors"
	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"time"
)

func (m *DataMsgModel) raiseHand(r *plugnmeet.DataMessageReq) error {
	participants, _ := m.lk.LoadParticipants(r.RoomId)

	var ids []string
	for _, participant := range participants {
		meta, err := m.lk.UnmarshalParticipantMetadata(participant.Metadata)
		if err != nil {
			continue
		}
		if meta.IsAdmin && (r.RequestedUserId != participant.Identity) {
			ids = append(ids, participant.Identity)
		}
	}

	reqPar, metadata, _ := m.lk.LoadParticipantWithMetadata(r.RoomId, r.RequestedUserId)

	// now update user's metadata
	metadata.RaisedHand = true

	_, err := m.lk.UpdateParticipantMetadataByStruct(r.RoomId, r.RequestedUserId, metadata)
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

func (m *DataMsgModel) lowerHand(r *plugnmeet.DataMessageReq) error {
	_, metadata, err := m.lk.LoadParticipantWithMetadata(r.RoomId, r.RequestedUserId)
	if err != nil {
		return err
	}

	// now update user's metadata
	metadata.RaisedHand = false

	_, err = m.lk.UpdateParticipantMetadataByStruct(r.RoomId, r.RequestedUserId, metadata)
	if err != nil {
		return err
	}

	return nil
}

func (m *DataMsgModel) otherUserLowerHand(r *plugnmeet.DataMessageReq) error {
	if !r.IsAdmin {
		return errors.New("only allow for admin")
	}
	userId := r.Msg

	_, metadata, err := m.lk.LoadParticipantWithMetadata(r.RoomId, userId)
	if err != nil {
		return err
	}

	// now update user's metadata
	metadata.RaisedHand = false

	_, err = m.lk.UpdateParticipantMetadataByStruct(r.RoomId, userId, metadata)
	if err != nil {
		return err
	}

	return nil
}
