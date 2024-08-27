package dbservice

import (
	"fmt"
	"testing"
	"time"
)

func TestDatabaseService_GetAnalytics(t *testing.T) {
	roomIds := []string{"test01"}
	analytics, total, err := s.GetAnalytics(roomIds, 0, 5, nil)
	if err != nil {
		t.Error(err)
	}

	t.Logf("%+v with total: %d", analytics, total)
}

func TestDatabaseService_GetAnalyticByFileId(t *testing.T) {
	analytic, err := s.GetAnalyticByFileId(analyticFileId)
	if err != nil {
		t.Error(err)
	}

	if analytic == nil {
		t.Error("got empty data but should contain data")
		return
	}
	t.Logf("%+v", analytic)

	analytic, err = s.GetAnalyticByFileId(fmt.Sprintf("%d", time.Now().UnixMilli()))
	if err != nil {
		t.Error(err)
	}
	if analytic != nil {
		t.Error("expected nil analytic but got something else")
	}
}

func TestDatabaseService_GetAnalyticByRoomTableId(t *testing.T) {
	analytic, err := s.GetAnalyticByRoomTableId(roomTableId)
	if err != nil {
		t.Error(err)
	}

	if analytic == nil {
		t.Error("got empty data but should contain data")
		return
	}
	t.Logf("%+v", analytic)

	analytic, err = s.GetAnalyticByRoomTableId(uint64(time.Now().Second()))
	if err != nil {
		t.Error(err)
	}
	if analytic != nil {
		t.Error("expected nil analytic but got something else")
	}
}

func TestDatabaseService_DeleteAnalyticByFileId(t *testing.T) {
	affected, err := s.DeleteAnalyticByFileId(analyticFileId)
	if err != nil {
		t.Error(err)
	}

	if affected == 0 {
		t.Error("should delete recording but got no affected recording")
		return
	}

	affected, err = s.DeleteAnalyticByFileId(fmt.Sprintf("%d", time.Now().UnixMilli()))
	if err != nil {
		t.Error(err)
	}

	if affected != 0 {
		t.Error("should not find recording but got affected recording")
		return
	}
}
