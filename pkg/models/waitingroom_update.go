package models

import (
	"errors"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	log "github.com/sirupsen/logrus"
)

func (m *WaitingRoomModel) ApproveWaitingUsers(r *plugnmeet.ApproveWaitingUsersReq) error {
	if r.UserId == "all" {
		participants, err := m.natsService.GetOnlineUsersList(r.RoomId)
		if err != nil {
			return err
		}

		for _, p := range participants {
			err = m.approveUser(r.RoomId, r.UserId, p.Metadata)
			log.Errorln(err)
		}

		return nil
	}

	p, err := m.natsService.GetUserInfo(r.RoomId, r.UserId)
	if err != nil {
		return err
	}

	return m.approveUser(r.RoomId, r.UserId, p.Metadata)
}

func (m *WaitingRoomModel) approveUser(roomId, userId, metadata string) error {
	mt, err := m.natsService.UnmarshalUserMetadata(metadata)
	if err != nil {
		return err
	}
	mt.WaitForApproval = false // this mean doesn't need to wait anymore

	err = m.natsService.UpdateAndBroadcastUserMetadata(roomId, userId, mt, nil)
	if err != nil {
		return errors.New("can't approve user. try again")
	}

	return nil
}

func (m *WaitingRoomModel) UpdateWaitingRoomMessage(r *plugnmeet.UpdateWaitingRoomMessageReq) error {
	roomMeta, err := m.natsService.GetRoomMetadataStruct(r.RoomId)
	if err != nil {
		return err
	}

	roomMeta.RoomFeatures.WaitingRoomFeatures.WaitingRoomMsg = r.Msg
	err = m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, roomMeta)

	return err
}
