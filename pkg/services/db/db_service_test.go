package dbservice

import (
	"fmt"
	"github.com/mynaparrot/plugnmeet-server/helpers"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
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
var sid = fmt.Sprintf("%d", time.Now().UnixMicro())
var roomTableId uint64
var roomId = "test01"
var roomCreationTime int64
var analyticFileId = fmt.Sprintf("file-%d", time.Now().Unix())

func init() {
	appCnf, err := helpers.ReadYamlConfigFile(root + "/config.yaml")
	if err != nil {
		panic(err)
	}

	appCnf.RootWorkingDir = root
	// set this config for global usage
	config.New(appCnf)

	// now prepare server
	err = helpers.PrepareServer(config.GetConfig())
	if err != nil {
		panic(err)
	}
	s = New(config.GetConfig().DB)
}

func TestDatabaseService_InsertOrUpdateRoomInfo(t *testing.T) {
	info := &dbmodels.RoomInfo{
		RoomId:       roomId,
		RoomTitle:    "Testing",
		Sid:          sid,
		IsRunning:    1,
		IsRecording:  0,
		IsActiveRtmp: 0,
	}

	_, err := s.InsertOrUpdateRoomInfo(info)
	if err != nil {
		t.Error(err)
	}

	t.Logf("%+v", info)
	roomTableId = info.ID
	roomCreationTime = info.CreationTime
	info.RoomTitle = "changed to testing"
	info.JoinedParticipants = 10

	_, err = s.InsertOrUpdateRoomInfo(info)
	if err != nil {
		t.Error(err)
	}

	t.Logf("%+v", info)
}

func TestDatabaseService_InsertAnalyticsData(t *testing.T) {
	info := &dbmodels.Analytics{
		RoomTableID: roomTableId,
		RoomID:      roomId,
		FileID:      analyticFileId,
		FileSize:    12.34,
	}

	_, err := s.InsertAnalyticsData(info)
	if err != nil {
		t.Error(err)
	}

	t.Logf("%+v", info)
}
