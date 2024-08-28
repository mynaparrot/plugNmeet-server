package models

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
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

	err := m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_SESSION_ENDED, r.GetRoomId(), "notifications.room-disconnected-room-ended", nil)
	if err != nil {
		// we'll just log error
		log.Errorln(err)
	}

	// change room status to delete
	err = m.natsService.UpdateRoomStatus(r.GetRoomId(), natsservice.RoomStatusEnded)
	if err != nil {
		log.Errorln(err)
	}

	_, err = m.lk.EndRoom(r.GetRoomId())
	if err != nil {
		// TODO: need think how to handle
		// because room may not cleaned up properly
		// we'll just log error
		log.Errorln(err)
	}

	_, _ = m.ds.UpdateRoomStatus(&dbmodels.RoomInfo{
		RoomId:    r.GetRoomId(),
		IsRunning: 0,
		Ended:     time.Now().UTC(),
	})

	// delete from room duration
	_ = m.rs.DeleteRoomWithDuration(r.GetRoomId())
	// TODO: process everything to clean up the room

	return true, "success"
}
