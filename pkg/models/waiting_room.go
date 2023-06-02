package models

import (
	"database/sql"
	"errors"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	log "github.com/sirupsen/logrus"
)

type UserWaitingRoomModel struct {
	db          *sql.DB
	roomService *RoomService
}

func NewWaitingRoomModel() *UserWaitingRoomModel {
	return &UserWaitingRoomModel{
		roomService: NewRoomService(),
	}
}

func (u *UserWaitingRoomModel) ApproveWaitingUsers(r *plugnmeet.ApproveWaitingUsersReq) error {
	if r.UserId == "all" {
		participants, err := u.roomService.LoadParticipants(r.RoomId)
		if err != nil {
			return err
		}

		for _, p := range participants {
			err = u.approveUser(r.RoomId, r.UserId, p.Metadata)
			log.Errorln(err)
		}

		return nil
	}

	p, err := u.roomService.LoadParticipantInfo(r.RoomId, r.UserId)
	if err != nil {
		return err
	}

	return u.approveUser(r.RoomId, r.UserId, p.Metadata)
}

func (u *UserWaitingRoomModel) approveUser(roomId, userId, metadata string) error {
	meta := make([]byte, len(metadata))
	copy(meta, metadata)

	m, _ := u.roomService.UnmarshalParticipantMetadata(string(meta))
	m.WaitForApproval = false // this mean doesn't need to wait anymore

	_, err := u.roomService.UpdateParticipantMetadataByStruct(roomId, userId, m)
	if err != nil {
		return errors.New("can't approve user. try again")
	}

	return nil
}

func (u *UserWaitingRoomModel) UpdateWaitingRoomMessage(r *plugnmeet.UpdateWaitingRoomMessageReq) error {
	_, roomMeta, err := u.roomService.LoadRoomWithMetadata(r.RoomId)
	if err != nil {
		return err
	}

	roomMeta.RoomFeatures.WaitingRoomFeatures.WaitingRoomMsg = r.Msg
	_, err = u.roomService.UpdateRoomMetadataByStruct(r.RoomId, roomMeta)

	return err
}
