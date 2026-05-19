package models

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"google.golang.org/protobuf/encoding/protojson"
)

func (m *BreakoutRoomModel) GetBreakoutRooms(roomId string) ([]*plugnmeet.BreakoutRoom, error) {
	breakoutRooms, err := m.fetchBreakoutRooms(roomId)
	if err != nil {
		return nil, err
	}

	if breakoutRooms == nil || len(breakoutRooms) == 0 {
		return nil, config.NoBreakoutRoomsFound
	}

	return breakoutRooms, nil
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
	for i, r := range rooms {
		room := new(plugnmeet.BreakoutRoom)
		err := protojson.Unmarshal([]byte(r), room)
		if err != nil {
			continue
		}
		room.Id = i
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
