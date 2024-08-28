package models

import (
	"errors"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
)

func (m *LtiV1Model) LTIV1JoinRoom(c *plugnmeet.LtiClaims) (string, error) {
	res, _ := m.rm.IsRoomActive(&plugnmeet.IsRoomActiveReq{
		RoomId: c.RoomId,
	})

	if !res.GetIsActive() {
		_, err := m.createRoomSession(c)
		if err != nil {
			return "", errors.New(err.Error())
		}
	}

	token, err := m.joinRoom(c)
	if err != nil {
		return "", err
	}

	return token, nil
}

func (m *LtiV1Model) createRoomSession(c *plugnmeet.LtiClaims) (*plugnmeet.ActiveRoomInfo, error) {
	req := utils.PrepareLTIV1RoomCreateReq(c)
	return m.rm.CreateRoom(req)
}

func (m *LtiV1Model) joinRoom(c *plugnmeet.LtiClaims) (string, error) {
	um := NewUserModel(m.app, m.ds, m.rs, nil)
	token, err := um.GetPNMJoinToken(&plugnmeet.GenerateTokenReq{
		RoomId: c.RoomId,
		UserInfo: &plugnmeet.UserInfo{
			UserId:  c.UserId,
			Name:    c.Name,
			IsAdmin: c.IsAdmin,
			UserMetadata: &plugnmeet.UserMetadata{
				IsAdmin: c.IsAdmin,
			},
		},
	})
	if err != nil {
		return "", err
	}

	return token, nil
}
