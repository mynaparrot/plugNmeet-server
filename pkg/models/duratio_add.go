package models

import (
	log "github.com/sirupsen/logrus"
)

type RoomDurationInfo struct {
	Duration  uint64 `redis:"duration"`
	StartedAt uint64 `redis:"startedAt"`
}

func (m *RoomDurationModel) AddRoomWithDurationInfo(roomId string, r *RoomDurationInfo) error {
	err := m.rs.AddRoomWithDurationInfo(roomId, r)
	if err != nil {
		log.Error(err)
		return err
	}
	return nil
}
