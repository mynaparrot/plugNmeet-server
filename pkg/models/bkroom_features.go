package models

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"
)

// BackToMainRoom issues a fresh parent-room join token for a participant
// returning from a breakout room. The parent comes from the client-supplied parent_room_id.
func (m *BreakoutRoomModel) BackToMainRoom(ctx context.Context, r *plugnmeet.BackToMainRoomReq) (string, error) {
	log := m.logger.WithFields(logrus.Fields{
		"breakoutRoomId": r.RoomId,
		"userId":         r.UserId,
		"parentRoomId":   r.ParentRoomId,
		"method":         "BackToMainRoom",
	})
	log.Infoln("request to return from breakout room to main room")

	// Bind the client-supplied parent room id to the caller's actual breakout:
	// breakout ids are <parentRoomId>-<n>, so the caller's room must be a child of it.
	if !strings.HasPrefix(r.RoomId, fmt.Sprintf(BreakoutRoomFormat, r.ParentRoomId, "")) {
		err := errors.New("parent room id does not match the caller's breakout room")
		log.WithError(err).Warn()
		return "", err
	}

	// Verify the parent room is still alive (a paused parent passes this check).
	mainRoom, mainMeta, err := m.natsService.GetRoomInfoWithMetadata(r.ParentRoomId)
	if err != nil {
		log.WithError(err).Error("failed to get main room info")
		return "", err
	}
	if mainRoom == nil || mainMeta == nil || !m.natsService.IsRoomStatusActive(mainRoom.Status) {
		err = errors.New("main room is not active anymore")
		log.WithError(err).Warn("main room not found or not active")
		return "", err
	}

	// User info comes from the main room's kv; breakout entries carry breakout-only state (e.g. isPresenter).
	p, pmeta, err := m.natsService.GetUserWithMetadata(r.ParentRoomId, r.UserId)
	if err != nil {
		log.WithError(err).Error("failed to get user info from main room")
		return "", err
	}
	if p == nil || pmeta == nil {
		err = errors.New("failed to get user info from main room")
		log.WithError(err).Error()
		return "", err
	}
	// let GetPNMJoinToken to set IsPresenter value
	pmeta.IsPresenter = false

	// Build the parent-room join token, mirrored from JoinBreakoutRoom.
	req := &plugnmeet.GenerateTokenReq{
		RoomId: r.ParentRoomId,
		UserInfo: &plugnmeet.UserInfo{
			UserId:       r.UserId,
			Name:         p.Name,
			IsAdmin:      pmeta.IsAdmin,
			UserMetadata: pmeta,
		},
	}
	token, err := m.um.GetPNMJoinToken(ctx, req, true)
	if err != nil {
		log.WithError(err).Error("failed to generate join token for main room")
		return "", err
	}

	log.Info("successfully generated join token for main room")
	return token, nil
}

// ReInviteBreakoutRoom re-sends the JOIN_BREAKOUT_ROOM system event to a user who
// is already assigned to the breakout room but has not joined.
func (m *BreakoutRoomModel) ReInviteBreakoutRoom(ctx context.Context, r *plugnmeet.ReInviteBreakoutRoomReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"parentRoomId":   r.RoomId,
		"breakoutRoomId": r.BreakoutRoomId,
		"userId":         r.UserId,
		"method":         "ReInviteBreakoutRoom",
	})
	log.Infoln("request to re-invite user to breakout room")

	room, err := m.fetchBreakoutRoom(r.RoomId, r.BreakoutRoomId)
	if err != nil {
		log.WithError(err).Error("failed to fetch breakout room info")
		return err
	}

	// verify the target user is actually assigned to this breakout room
	assigned := false
	for _, u := range room.Users {
		if u.Id == r.UserId {
			assigned = true
			break
		}
	}
	if !assigned {
		err = errors.New("user is not assigned to this breakout room")
		log.WithError(err).Warn()
		return err
	}

	// re-send the JOIN_BREAKOUT_ROOM invitation, exactly like CreateBreakoutRooms.
	err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_JOIN_BREAKOUT_ROOM, r.RoomId, r.BreakoutRoomId, &r.UserId)
	if err != nil {
		log.WithError(err).WithField("userId", r.UserId).Error("failed to re-send breakout room invitation")
		return err
	}

	log.Info("successfully re-sent breakout room invitation")
	return nil
}
