package models

import (
	"errors"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
)

func (m *BreakoutRoomModel) GetBreakoutRooms(roomId, userId string, isAdmin bool) ([]*plugnmeet.BreakoutRoom, error) {
	breakoutRooms, err := m.fetchBreakoutRooms(roomId)
	if err != nil {
		return nil, err
	}

	if breakoutRooms == nil || len(breakoutRooms) == 0 {
		return nil, errors.New("breakout-room.notifications.room-not-found")
	}

	if isAdmin {
		return breakoutRooms, nil
	}

	// Server-side filtering: non-admin callers receive all rooms only when
	// self-select is enabled on the parent room; otherwise they receive only
	// the room they are assigned to. A metadata fetch failure fails closed
	// (assigned room only), consistent with the join gate.
	allowSelfSelect := false
	if meta, mErr := m.natsService.GetRoomMetadataStruct(roomId); mErr == nil && meta != nil &&
		meta.RoomFeatures != nil && meta.RoomFeatures.BreakoutRoomFeatures != nil {
		allowSelfSelect = meta.RoomFeatures.BreakoutRoomFeatures.AllowSelfSelect
	}
	if allowSelfSelect {
		return breakoutRooms, nil
	}

	var filtered []*plugnmeet.BreakoutRoom
	for _, r := range breakoutRooms {
		for _, u := range r.Users {
			if u.Id == userId {
				filtered = append(filtered, r)
				break
			}
		}
	}
	return filtered, nil
}

func (m *BreakoutRoomModel) GetMyBreakoutRooms(roomId, userId string) (*plugnmeet.BreakoutRoom, error) {
	breakoutRooms, err := m.fetchBreakoutRooms(roomId)
	if err != nil {
		return nil, err
	}

	if breakoutRooms == nil || len(breakoutRooms) == 0 {
		return nil, errors.New("breakout-room.notifications.room-not-found")
	}

	for _, rr := range breakoutRooms {
		for _, u := range rr.Users {
			if u.Id == userId {
				return rr, nil
			}
		}
	}

	return nil, errors.New("breakout-room.notifications.room-not-found")
}

func (m *BreakoutRoomModel) fetchBreakoutRoom(roomId, breakoutRoomId string) (*plugnmeet.BreakoutRoom, error) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":         roomId,
		"breakoutRoomId": breakoutRoomId,
		"method":         "fetchBreakoutRoom",
	})

	result, err := m.rs.GetBreakoutRoom(roomId, breakoutRoomId)
	if err != nil {
		log.WithError(err).Error("failed to read breakout room from redis")
		return nil, errors.New("breakout-room.notifications.unexpected-error")
	}
	if result == "" {
		log.Warn("breakout room not found")
		return nil, errors.New("breakout-room.notifications.room-not-found")
	}

	room := new(plugnmeet.BreakoutRoom)
	err = protojson.Unmarshal([]byte(result), room)
	if err != nil {
		log.WithError(err).Error("failed to unmarshal breakout room")
		return nil, errors.New("breakout-room.notifications.unexpected-error")
	}

	return room, nil
}

func (m *BreakoutRoomModel) fetchBreakoutRooms(roomId string) ([]*plugnmeet.BreakoutRoom, error) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": roomId,
		"method": "fetchBreakoutRooms",
	})

	rooms, err := m.rs.GetAllBreakoutRoomsByParentRoomId(roomId)
	if err != nil {
		log.WithError(err).Error("failed to read breakout rooms from redis")
		return nil, errors.New("breakout-room.notifications.unexpected-error")
	}
	if rooms == nil || len(rooms) == 0 {
		return nil, nil
	}

	var breakoutRooms []*plugnmeet.BreakoutRoom
	for _, r := range rooms {
		room := new(plugnmeet.BreakoutRoom)
		err := protojson.Unmarshal([]byte(r), room)
		if err != nil {
			continue
		}
		for _, u := range room.Users {
			if room.Started {
				status, err := m.natsService.GetRoomUserStatus(room.Id, u.Id)
				if err == nil && status == natsservice.UserStatusOnline {
					u.Joined = true
				}
			}
		}
		breakoutRooms = append(breakoutRooms, room)
	}

	return breakoutRooms, nil
}

func (m *BreakoutRoomModel) ResolveParentRoomId(roomId string) string {
	meta, err := m.natsService.GetRoomMetadataStruct(roomId)
	if err != nil {
		m.logger.WithError(err).WithField("roomId", roomId).
			Warn("failed to load room metadata for parent resolution; falling back to token roomId")
		return roomId
	}
	if meta == nil {
		return roomId
	}
	if meta.IsBreakoutRoom && meta.ParentRoomId != "" {
		return meta.ParentRoomId
	}
	return roomId
}

// GetUnassignedBreakoutRoomUsers returns the users who are online in the MAIN room
// (identified by roomId) but are NOT present in any breakout room's Users list.
// These late-joining participants are invisible to the ASSIGNED-mode manage view and
// cannot self-join. Admins are intentionally included (owner decision) so they too can
// be reassigned. The returned list is meant for admins ONLY — callers must never expose
// it to non-admin participants (server-side filtering principle).
func (m *BreakoutRoomModel) GetUnassignedBreakoutRoomUsers(roomId string, rooms []*plugnmeet.BreakoutRoom) []*plugnmeet.BreakoutRoomUser {
	onlineUsers, err := m.natsService.GetOnlineUsersList(roomId)
	if err != nil || onlineUsers == nil {
		m.logger.WithError(err).WithField("roomId", roomId).
			Warn("failed to load online users for unassigned breakout-room users computation; returning empty")
		return nil
	}

	// Build a set of every user id assigned to any breakout room.
	assigned := make(map[string]struct{})
	for _, r := range rooms {
		for _, u := range r.Users {
			assigned[u.Id] = struct{}{}
		}
	}

	var unassigned []*plugnmeet.BreakoutRoomUser
	for _, u := range onlineUsers {
		// Skip system/internal identities (ingress, SIP, agent, TTS) and the
		// recorder/RTMP bots. They land in the main room's user bucket via
		// GetPNMJoinToken -> AddUser but are not real participants, so they must
		// never be offered as movable "unassigned" users. IsUserIdInternal mirrors
		// the existing user-listing filter (janitor_user.go); the recorder/RTMP
		// bots use reserved ids not covered by that prefix check, so exclude them
		// explicitly.
		if config.IsUserIdInternal(u.UserId) || u.UserId == config.RecorderBot || u.UserId == config.RtmpBot {
			continue
		}
		if _, ok := assigned[u.UserId]; ok {
			continue
		}
		// Joined is left at its default (false): it is meaningless for users who
		// are not in any breakout room yet.
		unassigned = append(unassigned, &plugnmeet.BreakoutRoomUser{
			Id:   u.UserId,
			Name: u.Name,
		})
	}
	return unassigned
}
