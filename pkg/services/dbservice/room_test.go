package dbservice

import (
	"fmt"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

var (
	_, b, _, _ = runtime.Caller(0)
	root       = filepath.Join(filepath.Dir(b), "../../..")
)

var s *DatabaseService

func init() {
	err := helpers.PrepareServer(root + "/config.yaml")
	if err != nil {
		panic(err)
	}
	s = NewDBService(config.AppCnf.ORM)
}
func TestDatabaseService_GetRoomInfoByRoomId(t *testing.T) {
	info, err := s.GetRoomInfoByRoomId("room01", 1)
	if err != nil {
		t.Error(err)
	}

	if info == nil {
		t.Log("success with empty data")
		return
	}

	t.Logf("%+v", info)
}

func TestDatabaseService_GetRoomInfoBySid(t *testing.T) {
	running := 1
	info, err := s.GetRoomInfoBySid("RM_kgSaR89fqjg6", &running)
	if err != nil {
		t.Error(err)
	}

	if info == nil {
		t.Log("success with empty data")
		return
	}

	t.Logf("%+v", info)
}

func TestDatabaseService_GetRoomInfoByTableId(t *testing.T) {
	info, err := s.GetRoomInfoByTableId(1016)
	if err != nil {
		t.Error(err)
	}

	if info == nil {
		t.Log("success with empty data")
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
		t.Log("success with empty data")
		return
	}

	t.Logf("%+v", rooms)
}

func TestDatabaseService_InsertOrUpdateRoomInfo(t *testing.T) {
	info := &dbmodels.RoomInfo{
		RoomId:       "test01",
		RoomTitle:    "Testing",
		Sid:          fmt.Sprintf("%d", time.Now().Unix()),
		IsRunning:    1,
		IsRecording:  1,
		IsActiveRtmp: 1,
		CreationTime: time.Now().Unix(),
	}

	_, err := s.InsertOrUpdateRoomInfo(info)
	if err != nil {
		t.Error(err)
	}

	t.Logf("%+v", info)
	info.RoomTitle = "changed to testing"
	info.JoinedParticipants = 10

	_, err = s.InsertOrUpdateRoomInfo(info)
	if err != nil {
		t.Error(err)
	}

	t.Logf("%+v", info)
}

func TestDatabaseService_UpdateRoomStatus(t *testing.T) {
	info := &dbmodels.RoomInfo{
		RoomId:    "test01",
		IsRunning: 0,
	}

	_, err := s.UpdateRoomStatus(info)
	if err != nil {
		t.Error(err)
	}

	t.Logf("%+v", info)
}
