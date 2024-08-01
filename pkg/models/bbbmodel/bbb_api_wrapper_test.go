package bbbmodel

import (
	"database/sql"
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/bbbapiwrapper"
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

var m *BBBApiWrapperModel
var sid = fmt.Sprintf("%d", time.Now().Unix())
var roomId = "test01"
var roomCreationTime int64
var recordId = fmt.Sprintf("rec-%d", time.Now().UnixMilli())

func init() {
	appCnf, err := helpers.ReadConfig(root + "/config.yaml")
	if err != nil {
		panic(err)
	}

	appCnf.RootWorkingDir = root
	// set this config for global usage
	config.NewAppConfig(appCnf)

	// now prepare server
	err = helpers.PrepareServer(config.GetConfig())
	if err != nil {
		panic(err)
	}
	m = NewBBBApiWrapperModel()

	info := &dbmodels.RoomInfo{
		RoomId:       roomId,
		RoomTitle:    "Testing",
		Sid:          sid,
		IsRunning:    1,
		IsRecording:  0,
		IsActiveRtmp: 0,
		Created:      time.Now().UTC(),
	}

	_, err = m.ds.InsertOrUpdateRoomInfo(info)
	if err != nil {
		panic(err)
	}

	v := sql.NullString{
		String: sid,
		Valid:  true,
	}
	data := &dbmodels.Recording{
		RecordID:         recordId,
		RoomID:           roomId,
		RoomSid:          v,
		Size:             10.10,
		RoomCreationTime: roomCreationTime,
	}

	_, err = m.ds.InsertRecordingData(data)
	if err != nil {
		panic(err)
	}
}

func TestBBBApiWrapperModel_GetRecordings(t *testing.T) {
	recordings, pag, err := m.GetRecordings("https://demo.plugnmeet.com", &bbbapiwrapper.GetRecordingsReq{
		MeetingID: roomId,
	})
	if err != nil {
		t.Error(err)
	}
	if len(recordings) == 0 {
		t.Error("should contains some data but got empty")
	}

	t.Logf("%+v, %+v", recordings[0], *pag)

	recordings, pag, err = m.GetRecordings("https://demo.plugnmeet.com", &bbbapiwrapper.GetRecordingsReq{
		RecordID: recordId,
	})
	if err != nil {
		t.Error(err)
	}
	if len(recordings) == 0 {
		t.Error("should contains some data but got empty")
	}

	t.Logf("%+v, %+v", recordings[0], *pag)
}
