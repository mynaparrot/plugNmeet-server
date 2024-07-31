package dbservice

import (
	"testing"
)

func TestDatabaseService_GetRecordings(t *testing.T) {
	roomIds := []string{"test01"}
	recordings, total, err := s.GetRecordings(roomIds, 0, 20, nil)
	if err != nil {
		t.Error(err)
	}

	t.Logf("%+v with total: %d", recordings, total)
}
