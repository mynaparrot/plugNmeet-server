package models

import (
	"context"
	"errors"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/sirupsen/logrus"
)

func (m *BreakoutRoomModel) JoinBreakoutRoom(ctx context.Context, r *plugnmeet.JoinBreakoutRoomReq) (string, error) {
	log := m.logger.WithFields(logrus.Fields{
		"parentRoomId":   r.RoomId,
		"breakoutRoomId": r.BreakoutRoomId,
		"userId":         r.UserId,
		"method":         "JoinBreakoutRoom",
	})
	log.Infoln("request to join breakout room")

	status, err := m.natsService.GetRoomUserStatus(r.BreakoutRoomId, r.UserId)
	if err != nil {
		log.WithError(err).Error("failed to get user status for breakout room")
		return "", err
	}
	if status == natsservice.UserStatusOnline {
		err = errors.New("user has already been joined")
		log.WithError(err).Warn()
		return "", err
	}

	room, err := m.fetchBreakoutRoom(r.RoomId, r.BreakoutRoomId)
	if err != nil {
		log.WithError(err).Error("failed to fetch breakout room info")
		return "", err
	}

	if !r.IsAdmin {
		canJoin := false
		for _, u := range room.Users {
			if u.Id == r.UserId {
				canJoin = true
				break
			}
		}
		if !canJoin {
			err = errors.New("user is not allowed to join this breakout room")
			log.WithError(err).Warn("user not in the list of allowed users for this breakout room")
			return "", err
		}
	}

	p, meta, err := m.natsService.GetUserWithMetadata(r.RoomId, r.UserId)
	if err != nil {
		log.WithError(err).Error("failed to get user info from parent room")
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
	token, err := m.um.GetPNMJoinToken(ctx, req)
	if err != nil {
		log.WithError(err).Error("failed to generate join token for breakout room")
		return "", err
	}

	log.Info("successfully generated join token for breakout room")
	return token, nil
}
