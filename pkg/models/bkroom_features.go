package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
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
		log.Warn("parent room id does not match the caller's breakout room")
		return "", errors.New("breakout-room.notifications.unexpected-error")
	}

	// Verify the parent room is still alive (a paused parent passes this check).
	mainRoom, mainMeta, err := m.natsService.GetRoomInfoWithMetadata(r.ParentRoomId)
	if err != nil {
		log.WithError(err).Error("failed to get main room info")
		return "", errors.New("breakout-room.notifications.unexpected-error")
	}
	if mainRoom == nil || mainMeta == nil || !m.natsService.IsRoomStatusActive(mainRoom.Status) {
		log.Warn("main room not found or not active")
		return "", errors.New("breakout-room.notifications.main-room-not-active")
	}

	// User info comes from the main room's kv; breakout entries carry breakout-only state (e.g. isPresenter).
	p, pmeta, err := m.natsService.GetUserWithMetadata(r.ParentRoomId, r.UserId)
	if err != nil {
		log.WithError(err).Error("failed to get user info from main room")
		return "", errors.New("breakout-room.notifications.unexpected-error")
	}
	if p == nil || pmeta == nil {
		log.Error("failed to get user info from main room")
		return "", errors.New("breakout-room.notifications.unexpected-error")
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
		return "", errors.New("breakout-room.notifications.unexpected-error")
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
		log.Error("failed to fetch breakout room info")
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
		log.Warn("user is not assigned to this breakout room")
		return errors.New("breakout-room.notifications.user-not-assigned")
	}

	// re-send the JOIN_BREAKOUT_ROOM invitation, exactly like CreateBreakoutRooms.
	err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_JOIN_BREAKOUT_ROOM, r.RoomId, r.BreakoutRoomId, &r.UserId)
	if err != nil {
		log.WithError(err).WithField("userId", r.UserId).Error("failed to re-send breakout room invitation")
		return errors.New("breakout-room.notifications.unexpected-error")
	}

	log.Info("successfully re-sent breakout room invitation")
	return nil
}

// MoveBreakoutRoomUser moves a participant to another breakout room (or to the
// main room when r.BreakoutRoomId is empty) while a session is in progress. The
// move is INSTANT: the user is reassigned in Redis, gets a fresh token for the
// target room, and — if currently online in a room OTHER than the target — is
// pushed a BREAKOUT_ROOM_USER_MOVED event carrying that token so the client can
// auto-redirect in the same tab. The flow validates the user, validates the
// target, verifies the user is actually online in a room OTHER than the target,
// reassigns in Redis, mints the target-room token, then pushes to the user's
// current room — guaranteeing a genuine room switch, never a same-room reload.
func (m *BreakoutRoomModel) MoveBreakoutRoomUser(ctx context.Context, r *plugnmeet.MoveBreakoutRoomUserReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"parentRoomId":         r.RoomId,
		"targetBreakoutRoomId": r.BreakoutRoomId,
		"userId":               r.UserId,
		"method":               "MoveBreakoutRoomUser",
	})
	log.Infoln("request to move user between breakout rooms")

	// Validate the user: pull their parent-room metadata so the new token
	// carries the correct identity (breakout entries carry breakout-only state).
	p, meta, err := m.natsService.GetUserWithMetadata(r.RoomId, r.UserId)
	if err != nil {
		log.WithError(err).Error("failed to get user info from parent room")
		return errors.New("breakout-room.notifications.unexpected-error")
	}
	if p == nil || meta == nil {
		log.Error("failed to get user info from parent room")
		return errors.New("breakout-room.notifications.unexpected-error")
	}

	// Validate the target breakout room (only when moving to a breakout room;
	// an empty id means move to the main room, which needs no validation).
	targetTitle := ""
	if r.BreakoutRoomId != "" {
		room, fErr := m.fetchBreakoutRoom(r.RoomId, r.BreakoutRoomId)
		if fErr != nil {
			log.Error("failed to fetch target breakout room info")
			return fErr
		}
		if room != nil {
			targetTitle = room.Title
		}
	}

	// Presence scan: find the room where the user is currently online so we can
	// both validate the move (the user must be online somewhere OTHER than the
	// target) and know where to deliver the move event (per-user, reliable
	// JetStream push) to the right client. There is exactly one scan here.
	currentRoomId := ""
	if status, sErr := m.natsService.GetRoomUserStatus(r.RoomId, r.UserId); sErr == nil && status == natsservice.UserStatusOnline {
		currentRoomId = r.RoomId
	}
	if currentRoomId == "" {
		if allRooms, gErr := m.rs.GetAllBreakoutRoomsByParentRoomId(r.RoomId); gErr == nil {
			for bkRoomId := range allRooms {
				if status, sErr := m.natsService.GetRoomUserStatus(bkRoomId, r.UserId); sErr == nil && status == natsservice.UserStatusOnline {
					currentRoomId = bkRoomId
					break
				}
			}
		} else {
			log.WithError(gErr).Warn("failed to fetch breakout rooms for presence scan; treating user as not found there")
		}
	}

	// Reject moves for users who are not online in any room (offline users are
	// not pushed and a same-room "move" would just reload and disconnect them).
	if currentRoomId == "" {
		log.Warn("user is not online in any room; rejecting move")
		return errors.New("breakout-room.notifications.user-not-online")
	}

	// Compute the target room id. breakout ids are always "<parentRoomId>-<n>"
	// so they can never equal the parent room id.
	targetRoomId := r.BreakoutRoomId
	if targetRoomId == "" {
		targetRoomId = r.RoomId
	}

	// Reject a move that would land the user in the room they are already in —
	// otherwise the client receives a token for its current room, calls
	// window.location.replace() on the same room, and silently disconnects.
	if currentRoomId == targetRoomId {
		if r.BreakoutRoomId == "" {
			log.Warn("user is already in the main room; rejecting move")
			return errors.New("breakout-room.notifications.user-already-in-main")
		}
		log.Warn("user is already in this breakout room; rejecting move")
		return errors.New("breakout-room.notifications.user-already-in-room")
	}

	// Reassign in Redis: removing from every other room (and, for a move to the
	// main room, from every breakout room).
	m.reassignUserToBreakoutRoom(r.RoomId, r.BreakoutRoomId, r.UserId, p.Name)

	// Mint a fresh token for the target room.
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
	// Move-to-main uses the parent room id and the "back to main" token path,
	// mirroring BackToMainRoom; a breakout target mirrors JoinBreakoutRoom.
	if r.BreakoutRoomId == "" {
		req.RoomId = r.RoomId
	}
	token, err := m.um.GetPNMJoinToken(ctx, req, r.BreakoutRoomId == "")
	if err != nil {
		log.WithError(err).Error("failed to generate token for target room")
		return errors.New("breakout-room.notifications.unexpected-error")
	}

	log.Info("successfully generated token for target room")

	j, mErr := json.Marshal(map[string]string{
		"token":        token,
		"targetRoomId": r.BreakoutRoomId,
		"title":        targetTitle,
	})
	if mErr != nil {
		log.WithError(mErr).Warn("failed to marshal move payload; assignment updated without push")
		return nil
	}

	if pErr := m.natsService.BroadcastSystemEventToRoom(
		plugnmeet.NatsMsgServerToClientEvents_BREAKOUT_ROOM_USER_MOVED,
		currentRoomId,
		string(j),
		&r.UserId,
	); pErr != nil {
		// The assignment and token are already done; a push failure must not
		// fail the move, so log a warning and return nil.
		log.WithError(pErr).Warn("failed to push BREAKOUT_ROOM_USER_MOVED; assignment updated without push")
		return nil
	}

	log.Info("successfully pushed BREAKOUT_ROOM_USER_MOVED")
	return nil
}
