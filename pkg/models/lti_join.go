package models

import (
	"context"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
)

func (m *LtiV1Model) LTIV1JoinRoom(ctx context.Context, c *plugnmeet.LtiClaims) (string, error) {
	res, _, _, _ := m.rm.IsRoomActive(ctx, &plugnmeet.IsRoomActiveReq{
		RoomId: c.RoomId,
	})

	if !res.GetIsActive() {
		_, err := m.createRoomSession(c)
		if err != nil {
			return "", err
		}
	}

	token, err := m.joinRoom(ctx, c)
	if err != nil {
		return "", err
	}

	return token, nil
}

func (m *LtiV1Model) createRoomSession(c *plugnmeet.LtiClaims) (*plugnmeet.ActiveRoomInfo, error) {
	req := utils.PrepareLTIV1RoomCreateReq(c)
	return m.rm.CreateRoom(req)
}

func (m *LtiV1Model) joinRoom(ctx context.Context, c *plugnmeet.LtiClaims) (string, error) {
	token, err := m.um.GetPNMJoinToken(ctx, &plugnmeet.GenerateTokenReq{
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
