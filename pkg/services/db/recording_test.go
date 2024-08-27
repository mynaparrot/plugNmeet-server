package dbservice

import (
	"database/sql"
	"fmt"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"testing"
	"time"
)

var recordId = fmt.Sprintf("%d", time.Now().UnixNano())

func TestDatabaseService_InsertRecordingData(t *testing.T) {
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

	_, err := s.InsertRecordingData(data)
	if err != nil {
		t.Error(err)
	}

	t.Logf("%+v", data)
}

func TestDatabaseService_GetRecordings(t *testing.T) {
	roomIds := []string{roomId}
	recordings, total, err := s.GetRecordings(roomIds, 0, 5, nil)
	if err != nil {
		t.Error(err)
	}

	t.Logf("%+v with total: %d", recordings, total)
}

func TestDatabaseService_GetRecording(t *testing.T) {
	recording, err := s.GetRecording(recordId)
	if err != nil {
		t.Error(err)
	}

	if recording == nil {
		t.Error("got empty data but should contain data")
		return
	}
	t.Logf("%+v", recording)

	recording, err = s.GetRecording(fmt.Sprintf("%d", time.Now().UnixMilli()))
	if err != nil {
		t.Error(err)
	}
	if recording != nil {
		t.Error("expected nil recording but got something else")
	}
}

func TestDatabaseService_DeleteRecording(t *testing.T) {
	affected, err := s.DeleteRecording(recordId)
	if err != nil {
		t.Error(err)
	}

	if affected == 0 {
		t.Error("should delete recording but got no affected recording")
		return
	}

	affected, err = s.DeleteRecording(fmt.Sprintf("%d", time.Now().UnixMilli()))
	if err != nil {
		t.Error(err)
	}

	if affected != 0 {
		t.Error("should not find recording but got affected recording")
		return
	}
}
