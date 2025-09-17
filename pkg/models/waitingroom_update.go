package models

import (
	"fmt"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

func (m *WaitingRoomModel) ApproveWaitingUsers(r *plugnmeet.ApproveWaitingUsersReq) error {
	if r.UserId == "all" {
		participants, err := m.natsService.GetOnlineUsersList(r.RoomId)
		if err != nil {
			return err
		}

		for _, p := range participants {
			err = m.approveUser(r.RoomId, r.UserId, p.Metadata)
			m.logger.WithError(err).Errorln("error approving user")
		}

		return nil
	}

	p, err := m.natsService.GetUserInfo(r.RoomId, r.UserId)
	if err != nil {
		return err
	}
	if p == nil {
		return fmt.Errorf("user not found")
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
		return fmt.Errorf("can't approve user. try again")
	}

	return nil
}

func (m *WaitingRoomModel) UpdateWaitingRoomMessage(r *plugnmeet.UpdateWaitingRoomMessageReq) error {
	roomMeta, err := m.natsService.GetRoomMetadataStruct(r.RoomId)
	if err != nil {
		return err
	}
	if roomMeta == nil {
		return fmt.Errorf("invalid nil room metadata information")
	}

	roomMeta.RoomFeatures.WaitingRoomFeatures.WaitingRoomMsg = r.Msg
	err = m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, roomMeta)

	return err
}
