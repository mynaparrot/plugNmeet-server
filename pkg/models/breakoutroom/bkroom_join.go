package breakoutroommodel

import (
	"errors"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

func (m *BreakoutRoomModel) JoinBreakoutRoom(r *plugnmeet.JoinBreakoutRoomReq) (string, error) {
	room, err := m.fetchBreakoutRoom(r.RoomId, r.BreakoutRoomId)
	if err != nil {
		return "", err
	}

	if !r.IsAdmin {
		canJoin := false
		for _, u := range room.Users {
			if u.Id == r.UserId {
				canJoin = true
			}
		}
		if !canJoin {
			return "", errors.New("you can't join in this room")
		}
	}

	p, meta, err := m.natsService.GetUserWithMetadata(r.UserId)
	if err != nil {
		return "", err
	}

	req := &plugnmeet.GenerateTokenReq{
		RoomId: r.BreakoutRoomId,
		UserInfo: &plugnmeet.UserInfo{
			UserId:       r.UserId,
			Name:         p.Name,
			IsAdmin:      meta.IsAdmin,
			UserMetadata: meta,
		},
	}

	token, err := m.rm.GetPNMJoinToken(req)
	if err != nil {
		return "", err
	}

	return token, nil
}
