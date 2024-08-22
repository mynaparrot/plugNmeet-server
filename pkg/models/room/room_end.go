package roommodel

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	log "github.com/sirupsen/logrus"
	"time"
)

func (m *RoomModel) EndRoom(r *plugnmeet.RoomEndReq) (bool, string) {
	// check first
	m.CheckAndWaitUntilRoomCreationInProgress(r.GetRoomId())

	roomDbInfo, _ := m.ds.GetRoomInfoByRoomId(r.GetRoomId(), 1)
	if roomDbInfo == nil || roomDbInfo.ID == 0 {
		return false, "room not active"
	}

	err := m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_SESSION_ENDED, r.GetRoomId(), "SESSION_ENDED_ON_REQUEST", nil)
	if err != nil {
		// we'll just log error
		log.Errorln(err)
	}

	_, err = m.lk.EndRoom(r.GetRoomId())
	if err != nil {
		// we'll just log error
		log.Errorln(err)
	}

	_, _ = m.ds.UpdateRoomStatus(&dbmodels.RoomInfo{
		RoomId:    r.GetRoomId(),
		IsRunning: 0,
		Ended:     time.Now().UTC(),
	})

	return true, "success"
}
