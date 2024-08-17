package dbservice

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"testing"
)

func TestDatabaseService_GetRoomInfoByRoomId(t *testing.T) {
	info, err := s.GetRoomInfoByRoomId(roomId, 1)
	if err != nil {
		t.Error(err)
	}

	if info == nil {
		t.Error("got empty data but should contain data")
		return
	}

	t.Logf("%+v", info)
}

func TestDatabaseService_GetRoomInfoBySid(t *testing.T) {
	running := 1
	info, err := s.GetRoomInfoBySid(sid, &running)
	if err != nil {
		t.Error(err)
	}

	if info == nil {
		t.Error("got empty data but should contain data")
		return
	}

	t.Logf("%+v", info)
}

func TestDatabaseService_GetRoomInfoByTableId(t *testing.T) {
	info, err := s.GetRoomInfoByTableId(roomTableId)
	if err != nil {
		t.Error(err)
	}

	if info == nil {
		t.Error("got empty data but should contain data")
		return
	}

	t.Logf("%+v", info)
}

func TestDatabaseService_GetActiveRoomsInfo(t *testing.T) {
	rooms, err := s.GetActiveRoomsInfo()
	if err != nil {
		t.Error(err)
	}

	if len(rooms) == 0 {
		t.Error("got empty data but should contain data")
		return
	}

	t.Logf("%+v", rooms)
}

func TestDatabaseService_UpdateRoomRecordingStatus(t *testing.T) {
	recorderId := "node01"
	_, err := s.UpdateRoomRecordingStatus(roomTableId, 1, &recorderId)
	if err != nil {
		t.Error(err)
	}

	_, err = s.UpdateRoomRecordingStatus(roomTableId, 0, nil)
	if err != nil {
		t.Error(err)
	}
}

func TestDatabaseService_UpdateRoomRTMPStatus(t *testing.T) {
	rtmpNodeId := "node01"
	_, err := s.UpdateRoomRTMPStatus(roomTableId, 1, &rtmpNodeId)
	if err != nil {
		t.Error(err)
	}

	_, err = s.UpdateRoomRTMPStatus(roomTableId, 0, nil)
	if err != nil {
		t.Error(err)
	}
}

func TestDatabaseService_IncrementOrDecrementNumParticipants(t *testing.T) {
	_, err := s.IncrementOrDecrementNumParticipants(sid, "+")
	if err != nil {
		t.Error(err)
	}

	_, err = s.IncrementOrDecrementNumParticipants(sid, "-")
	if err != nil {
		t.Error(err)
	}
}

func TestDatabaseService_UpdateRoomStatus(t *testing.T) {
	info := &dbmodels.RoomInfo{
		RoomId:    roomId,
		IsRunning: 0,
	}

	_, err := s.UpdateRoomStatus(info)
	if err != nil {
		t.Error(err)
	}

	info = &dbmodels.RoomInfo{
		Sid:       sid,
		IsRunning: 0,
	}

	_, err = s.UpdateRoomStatus(info)
	if err != nil {
		t.Error(err)
	}

	t.Logf("%+v", info)
}

func TestDatabaseService_GetPastRooms(t *testing.T) {
	rooms := []string{roomId}

	info, total, err := s.GetPastRooms(rooms, 0, 5, nil)
	if err != nil {
		t.Error(err)
	}

	t.Logf("%+v with total: %d", info, total)
}
