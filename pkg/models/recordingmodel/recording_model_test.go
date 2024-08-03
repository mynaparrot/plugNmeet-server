package recordingmodel

import (
	"database/sql"
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
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

var m *RecordingModel
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

	m = New(nil, nil, nil, nil)
	info := &dbmodels.RoomInfo{
		RoomId:       roomId,
		RoomTitle:    "Testing",
		Sid:          sid,
		IsRunning:    1,
		IsRecording:  0,
		IsActiveRtmp: 0,
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

func TestAuthRecording_FetchRecordings(t *testing.T) {
	result, err := m.FetchRecordings(&plugnmeet.FetchRecordingsReq{
		RoomIds: []string{roomId},
	})
	if err != nil {
		t.Error(err)
	}

	if len(result.RecordingsList) == 0 {
		t.Error("should contains some data but got empty")
	}

	t.Logf("%+v", result)
}

func TestAnalyticsAuthModel_FetchRecording(t *testing.T) {
	result, err := m.FetchRecording(recordId)
	if err != nil {
		t.Error(err)
	}

	if result == nil {
		t.Error("should contains some data but got empty")
	}

	t.Logf("%+v", result)

	_, err = m.FetchRecording(fmt.Sprintf("%d", time.Now().UnixMilli()))
	if err == nil {
		t.Error("should got not found error")
	}
}

func TestAnalyticsAuthModel_RecordingInfo(t *testing.T) {
	result, err := m.RecordingInfo(&plugnmeet.RecordingInfoReq{
		RecordId: recordId,
	})
	if err != nil {
		t.Error(err)
	}
	if result == nil {
		t.Error("should contains some data but got empty")
	}

	if result.RoomInfo.RoomSid != sid {
		t.Errorf("sid of %s should match with our %s", result.RoomInfo.RoomSid, sid)
	}

	t.Logf("%+v", result)
}

func TestAnalyticsAuthModel_DeleteRecording(t *testing.T) {
	err := m.DeleteRecording(&plugnmeet.DeleteRecordingReq{
		RecordId: recordId,
	})
	if err != nil {
		t.Error(err)
	}
}

func TestAnalyticsAuthModel_CreateAndVerifyToken(t *testing.T) {
	token, err := m.CreateTokenForDownload("test.mp4")
	if err != nil {
		t.Error(err)
	}
	_, res, err := m.VerifyRecordingToken(token)
	if err == nil {
		t.Error("should not found the file")
		return
	}

	if res != fiber.StatusNotFound {
		t.Errorf("should get response: %d", fiber.StatusNotFound)
	}

	t.Logf("%+v, response: %d", err, fiber.StatusNotFound)
}
