package models

import (
	"database/sql"
	"errors"
	"github.com/goccy/go-json"
)

type userWaitingRoomModel struct {
	db          *sql.DB
	roomService *RoomService
}

func NewWaitingRoomModel() *userWaitingRoomModel {
	return &userWaitingRoomModel{
		roomService: NewRoomService(),
	}
}

type ApproveWaitingUsersReq struct {
	RoomId string
	UserId string `json:"user_id" validate:"required"`
}

func (u *userWaitingRoomModel) ApproveWaitingUsers(r *ApproveWaitingUsersReq) error {
	if r.UserId == "all" {
		participants, err := u.roomService.LoadParticipantsFromRedis(r.RoomId)
		if err != nil {
			return err
		}

		for _, p := range participants {
			_ = u.approveUser(r.RoomId, r.UserId, p.Metadata)
		}

		return nil
	}

	p, err := u.roomService.LoadParticipantInfoFromRedis(r.RoomId, r.UserId)
	if err != nil {
		return err
	}

	return u.approveUser(r.RoomId, r.UserId, p.Metadata)
}

func (u *userWaitingRoomModel) approveUser(roomId, userId, metadata string) error {
	meta := make([]byte, len(metadata))
	copy(meta, metadata)

	m := new(UserMetadata)
	_ = json.Unmarshal(meta, m)
	m.WaitForApproval = false // this mean doesn't need to wait anymore

	newMeta, err := json.Marshal(m)
	if err != nil {
		return err
	}

	_, err = u.roomService.UpdateParticipantMetadata(roomId, userId, string(newMeta))
	if err != nil {
		return errors.New("can't approve user. try again")
	}

	return nil
}

type UpdateWaitingRoomMessageReq struct {
	RoomId string
	Msg    string `json:"msg" validate:"required"`
}

func (u *userWaitingRoomModel) UpdateWaitingRoomMessage(r *UpdateWaitingRoomMessageReq) error {
	_, roomMeta, err := u.roomService.LoadRoomWithMetadata(r.RoomId)
	if err != nil {
		return err
	}

	roomMeta.Features.WaitingRoomFeatures.WaitingRoomMsg = r.Msg
	_, err = u.roomService.UpdateRoomMetadataByStruct(r.RoomId, roomMeta)

	return err
}
