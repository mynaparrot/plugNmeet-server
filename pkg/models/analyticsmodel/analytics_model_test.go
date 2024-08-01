package analyticsmodel

import (
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

var (
	_, b, _, _ = runtime.Caller(0)
	root       = filepath.Join(filepath.Dir(b), "../../..")
)

var m *AnalyticsModel
var sid = fmt.Sprintf("%d", time.Now().Unix())
var roomId = "test01"
var roomTableId uint64
var fileId = fmt.Sprintf("%d", time.Now().Unix())

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
}

func TestAnalyticsAuthModel_AddAnalyticsFileToDB(t *testing.T) {
	info := &dbmodels.RoomInfo{
		RoomId:       roomId,
		RoomTitle:    "Testing",
		Sid:          sid,
		IsRunning:    1,
		IsRecording:  0,
		IsActiveRtmp: 0,
	}

	_, err := m.ds.InsertOrUpdateRoomInfo(info)
	if err != nil {
		t.Error(err)
	}

	t.Logf("%+v", info)
	roomTableId = info.ID
	stat, err := os.Stat(root + "/config.yaml")
	if err != nil {
		t.Error(err)
	}

	_, err = m.AddAnalyticsFileToDB(info.ID, info.CreationTime, info.RoomId, fileId, stat)
	if err != nil {
		t.Error(err)
	}
}

func TestAnalyticsAuthModel_FetchAnalytics(t *testing.T) {
	result, err := m.FetchAnalytics(&plugnmeet.FetchAnalyticsReq{
		RoomIds: []string{roomId},
	})
	if err != nil {
		t.Error(err)
	}

	if len(result.AnalyticsList) == 0 {
		t.Error("should contains some data but got emptry ")
	}

	t.Logf("%+v", result)
}

func TestAnalyticsAuthModel_fetchAnalytic(t *testing.T) {
	result, err := m.fetchAnalytic(fileId)
	if err != nil {
		t.Error(err)
	}

	if result == nil {
		t.Error("should contains some data but got emptry ")
	}

	t.Logf("%+v", result)

	_, err = m.fetchAnalytic(fmt.Sprintf("%d", time.Now().UnixMilli()))
	if err == nil {
		t.Error("should got not found error")
	}
}

func TestAnalyticsAuthModel_getAnalyticByRoomTableId(t *testing.T) {
	result, err := m.getAnalyticByRoomTableId(roomTableId)
	if err != nil {
		t.Error(err)
	}

	if result == nil {
		t.Error("should contains some data but got empty")
	}

	t.Logf("%+v", result)

	_, err = m.getAnalyticByRoomTableId(uint64(time.Now().UnixMilli()))
	if err == nil {
		t.Error("should got not found error")
	}
}

func TestAnalyticsAuthModel_DeleteAnalytics(t *testing.T) {
	err := m.DeleteAnalytics(&plugnmeet.DeleteAnalyticsReq{
		FileId: fileId,
	})
	if err != nil {
		t.Error(err)
	}
}

func TestAnalyticsAuthModel_generateTokenAndVerify(t *testing.T) {
	token, err := m.generateToken("test.json")
	if err != nil {
		t.Error(err)
	}

	_, res, err := m.VerifyAnalyticsToken(token)
	if err == nil {
		t.Error("should not found the file")
		return
	}

	if res != fiber.StatusNotFound {
		t.Errorf("should get response: %d", fiber.StatusNotFound)
	}

	t.Logf("%+v, response: %d", err, fiber.StatusNotFound)
}
