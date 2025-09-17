package models

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
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

	if rooms == nil || len(rooms) == 0 {
		return nil
	}

	for _, rr := range rooms {
		err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_SYSTEM_CHAT_MSG, rr.Id, r.Msg, nil)
		if err != nil {
			m.logger.Errorln(err)
		}
	}

	return nil
}
