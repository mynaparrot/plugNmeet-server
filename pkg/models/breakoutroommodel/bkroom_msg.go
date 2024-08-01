package breakoutroommodel

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
)

type SendBreakoutRoomMsgReq struct {
	RoomId string
	Msg    string `json:"msg" validate:"required"`
}

func (m *BreakoutRoomModel) SendBreakoutRoomMsg(r *plugnmeet.BroadcastBreakoutRoomMsgReq) error {
	rooms, err := m.fetchBreakoutRooms(r.RoomId)
	if err != nil {
		return err
	}

	for _, rr := range rooms {
		err = m.broadcastNotification(rr.Id, "system", "", r.Msg, plugnmeet.DataMsgType_USER, plugnmeet.DataMsgBodyType_CHAT, true)
		if err != nil {
			continue
		}
	}

	return nil
}

func (m *BreakoutRoomModel) broadcastNotification(roomId, fromUserId, toUserId, broadcastMsg string, typeMsg plugnmeet.DataMsgType, mType plugnmeet.DataMsgBodyType, isAdmin bool) error {
	payload := &plugnmeet.DataMessage{
		Type:   typeMsg,
		RoomId: roomId,
		Body: &plugnmeet.DataMsgBody{
			Type: mType,
			From: &plugnmeet.DataMsgReqFrom{
				UserId: fromUserId,
			},
			Msg: broadcastMsg,
		},
	}
	if toUserId != "" {
		payload.To = &toUserId
	}

	msg := &models.WebsocketToRedis{
		Type:    "sendMsg",
		DataMsg: payload,
		RoomId:  roomId,
		IsAdmin: isAdmin,
	}
	models.DistributeWebsocketMsgToRedisChannel(msg)

	return nil
}
