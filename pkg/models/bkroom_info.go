package models

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"google.golang.org/protobuf/encoding/protojson"
)

func (m *BreakoutRoomModel) GetBreakoutRooms(roomId, userId string, isAdmin bool) ([]*plugnmeet.BreakoutRoom, error) {
	breakoutRooms, err := m.fetchBreakoutRooms(roomId)
	if err != nil {
		return nil, err
	}

	if breakoutRooms == nil || len(breakoutRooms) == 0 {
		return nil, config.NoBreakoutRoomsFound
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
		return nil, config.NoBreakoutRoomsFound
	}

	for _, rr := range breakoutRooms {
		for _, u := range rr.Users {
			if u.Id == userId {
				return rr, nil
			}
		}
	}

	return nil, config.NotFoundErr
}

func (m *BreakoutRoomModel) fetchBreakoutRoom(roomId, breakoutRoomId string) (*plugnmeet.BreakoutRoom, error) {
	result, err := m.rs.GetBreakoutRoom(roomId, breakoutRoomId)
	if err != nil {
		return nil, err
	}
	if result == "" {
		return nil, config.NotFoundErr
	}

	room := new(plugnmeet.BreakoutRoom)
	err = protojson.Unmarshal([]byte(result), room)
	if err != nil {
		return nil, err
	}

	return room, nil
}

func (m *BreakoutRoomModel) fetchBreakoutRooms(roomId string) ([]*plugnmeet.BreakoutRoom, error) {
	rooms, err := m.rs.GetAllBreakoutRoomsByParentRoomId(roomId)
	if err != nil {
		return nil, err
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
