package models

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"
)

type SendBreakoutRoomMsgReq struct {
	RoomId string
	Msg    string `json:"msg" validate:"required"`
}

func (m *BreakoutRoomModel) SendBreakoutRoomMsg(r *plugnmeet.BroadcastBreakoutRoomMsgReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"parentRoomId": r.RoomId,
		"method":       "SendBreakoutRoomMsg",
	})
	log.Infoln("request to send message to all breakout rooms")

	rooms, err := m.fetchBreakoutRooms(r.RoomId)
	if err != nil {
		log.WithError(err).Error("failed to fetch breakout rooms")
		return err
	}

	if rooms == nil || len(rooms) == 0 {
		log.Info("no active breakout rooms found to send message")
		return nil
	}

	for _, rr := range rooms {
		err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_SYSTEM_CHAT_MSG, rr.Id, r.Msg, nil)
		if err != nil {
			log.WithError(err).WithField("breakoutRoomId", rr.Id).Error("failed to broadcast message to breakout room")
		}
	}

	log.Info("successfully broadcasted message to all breakout rooms")
	return nil
}
