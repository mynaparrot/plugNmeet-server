package dbmodels

import (
	"database/sql"
	"time"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

type Recording struct {
	ID               uint64         `gorm:"column:id;primaryKey;autoIncrement"`
	RecordID         string         `gorm:"column:record_id;unique;NOT NULL"`
	RoomID           string         `gorm:"column:room_id;NOT NULL"`
	RoomSid          sql.NullString `gorm:"column:room_sid;unique"`
	RecorderID       string         `gorm:"column:recorder_id;NOT NULL"`
	FilePath         string         `gorm:"column:file_path;NOT NULL"`
	Size             float64        `gorm:"column:size;NOT NULL"`
	Published        int64          `gorm:"column:published;default:1;NOT NULL"`
	CreationTime     int64          `gorm:"column:creation_time;autoCreateTime;NOT NULL"`
	RoomCreationTime int64          `gorm:"column:room_creation_time;default:0;NOT NULL"`
	Created          time.Time      `gorm:"column:created;autoCreateTime;NOT NULL"`
	Modified         time.Time      `gorm:"column:modified;autoUpdateTime;NOT NULL"`
}

func (m *Recording) TableName() string {
	return config.FormatDBTable("recordings")
}
