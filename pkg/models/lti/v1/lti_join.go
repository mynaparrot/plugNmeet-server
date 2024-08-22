package ltiv1model

import (
	"errors"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	usermodel "github.com/mynaparrot/plugnmeet-server/pkg/models/user"
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

func (m *LtiV1Model) createRoomSession(c *plugnmeet.LtiClaims) (*plugnmeet.NatsKvRoomInfo, error) {
	req := utils.PrepareLTIV1RoomCreateReq(c)
	return m.rm.CreateRoom(req)
}

func (m *LtiV1Model) joinRoom(c *plugnmeet.LtiClaims) (string, error) {
	um := usermodel.New(m.app, m.ds, m.rs, m.lk)
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
