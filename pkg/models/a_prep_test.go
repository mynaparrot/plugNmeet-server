package models

import (
	"database/sql"
	"fmt"
	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-server/helpers"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	dbservice "github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

var (
	_, b, _, _ = runtime.Caller(0)
	root       = filepath.Join(filepath.Dir(b), "../..")
)

var roomTableId uint64
var sid = uuid.NewString()
var roomId = "test01"
var roomCreationTime int64
var recordId string

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
}

func Test_PrepareModel(t *testing.T) {
	ds := dbservice.New(config.GetConfig().DB)
	info := &dbmodels.RoomInfo{
		RoomId:       roomId,
		RoomTitle:    "Testing",
		Sid:          sid,
		IsRunning:    1,
		IsRecording:  0,
		IsActiveRtmp: 0,
	}

	_, err := ds.InsertOrUpdateRoomInfo(info)
	if err != nil {
		panic(err)
	}

	roomTableId = info.ID
	roomCreationTime = info.CreationTime
	t.Logf("%+v", info)

	v := sql.NullString{
		String: sid,
		Valid:  true,
	}

	recordId = fmt.Sprintf("%s-%d", info.Sid, time.Now().UnixMilli())
	data := &dbmodels.Recording{
		RecordID:         recordId,
		RoomID:           roomId,
		RoomSid:          v,
		Size:             10.10,
		RoomCreationTime: roomCreationTime,
	}

	_, err = ds.InsertRecordingData(data)
	if err != nil {
		panic(err)
	}

	t.Logf("%+v", data)
}
