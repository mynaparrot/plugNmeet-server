package roomdurationmodel

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redisservice"
	log "github.com/sirupsen/logrus"
	"strings"
)

func (m *RoomDurationModel) GetRoomDurationInfo(roomId string) (*RoomDurationInfo, error) {
	val := new(RoomDurationInfo)
	err := m.rs.GetRoomWithDurationInfo(roomId, val)
	if err != nil {
		return nil, err
	}
	return val, nil
}

func (m *RoomDurationModel) GetRoomsWithDurationMap() map[string]RoomDurationInfo {
	roomsKey, err := m.rs.GetRoomsWithDurationKeys()
	if err != nil {
		return nil
	}
	out := make(map[string]RoomDurationInfo)
	for _, key := range roomsKey {
		var val RoomDurationInfo
		err = m.rs.GetRoomWithDurationInfoByKey(key, &val)
		if err != nil {
			log.Errorln(err)
			continue
		}

		rId := strings.Replace(key, redisservice.RoomWithDurationInfoKey+":", "", 1)
		out[rId] = val
	}

	return out
}
