package models

import (
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"os"
	"testing"
	"time"
)

var analyticsModel *AnalyticsModel
var fileId = fmt.Sprintf("%d", time.Now().UnixNano())

func init() {
	analyticsModel = NewAnalyticsModel(nil, nil, nil)
}

func TestAnalyticsAuthModel_AddAnalyticsFileToDB(t *testing.T) {
	stat, err := os.Stat(root + "/config.yaml")
	if err != nil {
		t.Error(err)
	}

	_, err = analyticsModel.AddAnalyticsFileToDB(roomTableId, roomCreationTime, roomId, fileId, stat)
	if err != nil {
		t.Error(err)
	}
}

func TestAnalyticsAuthModel_FetchAnalytics(t *testing.T) {
	result, err := analyticsModel.FetchAnalytics(&plugnmeet.FetchAnalyticsReq{
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
	result, err := analyticsModel.fetchAnalytic(fileId)
	if err != nil {
		t.Error(err)
	}

	if result == nil {
		t.Error("should contains some data but got emptry ")
	}

	t.Logf("%+v", result)

	_, err = analyticsModel.fetchAnalytic(fmt.Sprintf("%d", time.Now().UnixMilli()))
	if err == nil {
		t.Error("should got not found error")
	}
}

func TestAnalyticsAuthModel_getAnalyticByRoomTableId(t *testing.T) {
	result, err := analyticsModel.getAnalyticByRoomTableId(roomTableId)
	if err != nil {
		t.Error(err)
	}

	if result == nil {
		t.Error("should contains some data but got empty")
	}

	t.Logf("%+v", result)

	_, err = analyticsModel.getAnalyticByRoomTableId(uint64(time.Now().UnixMilli()))
	if err == nil {
		t.Error("should got not found error")
	}
}

func TestAnalyticsAuthModel_DeleteAnalytics(t *testing.T) {
	err := analyticsModel.DeleteAnalytics(&plugnmeet.DeleteAnalyticsReq{
		FileId: fileId,
	})
	if err != nil {
		t.Error(err)
	}
}

func TestAnalyticsAuthModel_generateTokenAndVerify(t *testing.T) {
	token, err := analyticsModel.generateToken("test.json")
	if err != nil {
		t.Error(err)
	}

	_, res, err := analyticsModel.VerifyAnalyticsToken(token)
	if err == nil {
		t.Error("should not found the file")
		return
	}

	if res != fiber.StatusNotFound {
		t.Errorf("should get response: %d", fiber.StatusNotFound)
	}

	t.Logf("%+v, response: %d", err, fiber.StatusNotFound)
}
