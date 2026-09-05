package models

import (
	"context"
	"errors"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
)

func (m *BreakoutRoomModel) JoinBreakoutRoom(ctx context.Context, r *plugnmeet.JoinBreakoutRoomReq, isAdmin bool) (string, error) {
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
		return "", errors.New("breakout-room.notifications.unexpected-error")
	}
	if status == natsservice.UserStatusOnline {
		log.Warn("user has already been joined")
		return "", errors.New("breakout-room.notifications.user-already-joined")
	}

	room, err := m.fetchBreakoutRoom(r.RoomId, r.BreakoutRoomId)
	if err != nil {
		log.Error("failed to fetch breakout room info")
		return "", err
	}

	// Fetch the parent room metadata to know whether self-select is enabled.
	// A metadata fetch failure must fail CLOSED: the membership gate stays
	// enforced and the join proceeds only for assigned users. We never abort
	// the join on a metadata error — just log a warning.
	parentMeta, pmErr := m.natsService.GetRoomMetadataStruct(r.RoomId)
	allowSelfSelect := false
	if pmErr != nil {
		log.WithError(pmErr).Warn("failed to load parent room metadata; self-select disabled for this join")
	} else if parentMeta != nil &&
		parentMeta.RoomFeatures != nil &&
		parentMeta.RoomFeatures.BreakoutRoomFeatures != nil {
		allowSelfSelect = parentMeta.RoomFeatures.BreakoutRoomFeatures.AllowSelfSelect
	}

	if !isAdmin && !allowSelfSelect {
		canJoin := false
		for _, u := range room.Users {
			if u.Id == r.UserId {
				canJoin = true
				break
			}
		}
		if !canJoin {
			log.Warn("user not in the list of allowed users for this breakout room")
			return "", errors.New("breakout-room.notifications.user-not-allowed-join")
		}
	}

	p, meta, err := m.natsService.GetUserWithMetadata(r.RoomId, r.UserId)
	if err != nil {
		log.WithError(err).Error("failed to get user info from parent room")
		return "", errors.New("breakout-room.notifications.unexpected-error")
	}

	if p == nil || meta == nil {
		log.Error("failed to get user info from parent room")
		return "", errors.New("breakout-room.notifications.unexpected-error")
	}
	// let GetPNMJoinToken to set IsPresenter value
	meta.IsPresenter = false

	req := &plugnmeet.GenerateTokenReq{
		RoomId: r.BreakoutRoomId,
		UserInfo: &plugnmeet.UserInfo{
			UserId:       r.UserId,
			Name:         p.Name,
			IsAdmin:      meta.IsAdmin,
			UserMetadata: meta,
		},
	}
	token, err := m.um.GetPNMJoinToken(ctx, req, false)
	if err != nil {
		log.WithError(err).Error("failed to generate join token for breakout room")
		return "", errors.New("breakout-room.notifications.unexpected-error")
	}

	log.Info("successfully generated join token for breakout room")

	// Under self-select the user may join any breakout room, so keep the Redis
	// room assignment accurate: remove them from any other room's user list and
	// add them to the target room. This is best-effort — the token is already
	// minted and the join must not fail because of a Redis update problem.
	if allowSelfSelect {
		m.reassignUserToBreakoutRoom(r.RoomId, r.BreakoutRoomId, r.UserId, p.Name)
	}

	return token, nil
}

// reassignUserToBreakoutRoom keeps the Redis breakout-room assignments accurate
// for both self-select joins AND admin moves: the user is removed from any other
// breakout room's user list and added to the target room's user list (if not
// already present).
// Errors are non-fatal — the caller has already minted the join token and the
// join must not fail because of a Redis update problem.
func (m *BreakoutRoomModel) reassignUserToBreakoutRoom(parentRoomId, breakoutRoomId, userId, name string) {
	log := m.logger.WithFields(logrus.Fields{
		"parentRoomId":   parentRoomId,
		"breakoutRoomId": breakoutRoomId,
		"userId":         userId,
		"method":         "reassignUserToBreakoutRoom",
	})

	// Move-to-main case (used by MoveBreakoutRoomUser): remove the user from
	// EVERY breakout room's user list and add them to none. The join flow never
	// passes an empty breakoutRoomId, so this branch is exclusive to moves.
	if breakoutRoomId == "" {
		allRooms, err := m.rs.GetAllBreakoutRoomsByParentRoomId(parentRoomId)
		if err != nil {
			log.WithError(err).Warn("failed to load breakout rooms for move-to-main reassignment")
			return
		}
		for _, raw := range allRooms {
			br := new(plugnmeet.BreakoutRoom)
			if uErr := protojson.Unmarshal([]byte(raw), br); uErr != nil {
				log.WithError(uErr).Warn("failed to unmarshal breakout room during reassignment; skipping")
				continue
			}
			found := false
			idx := -1
			for i, u := range br.Users {
				if u.Id == userId {
					found = true
					idx = i
					break
				}
			}
			if !found {
				continue
			}
			br.Users = append(br.Users[:idx], br.Users[idx+1:]...)
			marshal, mErr := protojson.Marshal(br)
			if mErr != nil {
				log.WithError(mErr).Warn("failed to marshal breakout room during reassignment; skipping")
				continue
			}
			if iErr := m.rs.InsertOrUpdateBreakoutRoom(parentRoomId, br.Id, marshal); iErr != nil {
				log.WithError(iErr).Warn("failed to update breakout room assignment during reassignment; skipping")
				continue
			}
		}
		return
	}

	allRooms, err := m.rs.GetAllBreakoutRoomsByParentRoomId(parentRoomId)
	if err != nil {
		log.WithError(err).Warn("failed to load breakout rooms for reassignment")
		return
	}

	for _, raw := range allRooms {
		br := new(plugnmeet.BreakoutRoom)
		if uErr := protojson.Unmarshal([]byte(raw), br); uErr != nil {
			log.WithError(uErr).Warn("failed to unmarshal breakout room during reassignment; skipping")
			continue
		}

		if br.Id == breakoutRoomId {
			// target room: ensure the user appears in its user list.
			already := false
			for _, u := range br.Users {
				if u.Id == userId {
					already = true
					break
				}
			}
			if already {
				continue
			}
			br.Users = append(br.Users, &plugnmeet.BreakoutRoomUser{
				Id:   userId,
				Name: name,
			})
		} else {
			// a different room: if the user was listed here, remove them.
			found := false
			idx := -1
			for i, u := range br.Users {
				if u.Id == userId {
					found = true
					idx = i
					break
				}
			}
			if !found {
				continue
			}
			br.Users = append(br.Users[:idx], br.Users[idx+1:]...)
		}

		marshal, mErr := protojson.Marshal(br)
		if mErr != nil {
			log.WithError(mErr).Warn("failed to marshal breakout room during reassignment; skipping")
			continue
		}
		if iErr := m.rs.InsertOrUpdateBreakoutRoom(parentRoomId, br.Id, marshal); iErr != nil {
			log.WithError(iErr).Warn("failed to update breakout room assignment during reassignment; skipping")
			continue
		}
	}
}
