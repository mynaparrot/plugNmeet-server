package roommodel

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"time"
)

func (m *RoomModel) EndRoom(r *plugnmeet.RoomEndReq) (bool, string) {
	// check first
	m.CheckAndWaitUntilRoomCreationInProgress(r.GetRoomId())

	roomDbInfo, _ := m.ds.GetRoomInfoByRoomId(r.GetRoomId(), 1)
	if roomDbInfo == nil || roomDbInfo.ID == 0 {
		return false, "room not active"
	}

	_, err := m.lk.EndRoom(r.GetRoomId())
	if err != nil {
		return false, "can't end room"
	}

	_, _ = m.ds.UpdateRoomStatus(&dbmodels.RoomInfo{
		RoomId:    r.GetRoomId(),
		IsRunning: 0,
		Ended:     time.Now().UTC(),
	})

	return true, "success"
}
