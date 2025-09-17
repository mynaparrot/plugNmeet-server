package models

import (
	"os"

	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
)

func (m *AnalyticsModel) AddAnalyticsFileToDB(roomTableId uint64, roomCreationTime int64, roomId, fileId string, stat os.FileInfo) (int64, error) {
	fSize := float64(stat.Size())
	// we'll convert bytes to KB
	if fSize > 1000 {
		fSize = fSize / 1000.0
	} else {
		fSize = 1
	}

	info := &dbmodels.Analytics{
		RoomTableID:      roomTableId,
		RoomID:           roomId,
		FileID:           fileId,
		FileName:         fileId + ".json",
		FileSize:         fSize,
		RoomCreationTime: roomCreationTime,
	}

	return m.ds.InsertAnalyticsData(info)
}
