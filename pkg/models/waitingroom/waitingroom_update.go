package waitingroommodel

import (
	"errors"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	log "github.com/sirupsen/logrus"
)

func (m *WaitingRoomModel) ApproveWaitingUsers(r *plugnmeet.ApproveWaitingUsersReq) error {
	if r.UserId == "all" {
		participants, err := m.lk.LoadParticipants(r.RoomId)
		if err != nil {
			return err
		}

		for _, p := range participants {
			err = m.approveUser(r.RoomId, r.UserId, p.Metadata)
			log.Errorln(err)
		}

		return nil
	}

	p, err := m.lk.LoadParticipantInfo(r.RoomId, r.UserId)
	if err != nil {
		return err
	}

	return m.approveUser(r.RoomId, r.UserId, p.Metadata)
}

func (m *WaitingRoomModel) approveUser(roomId, userId, metadata string) error {
	meta := make([]byte, len(metadata))
	copy(meta, metadata)

	mm, _ := m.lk.UnmarshalParticipantMetadata(string(meta))
	mm.WaitForApproval = false // this mean doesn't need to wait anymore

	_, err := m.lk.UpdateParticipantMetadataByStruct(roomId, userId, mm)
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
