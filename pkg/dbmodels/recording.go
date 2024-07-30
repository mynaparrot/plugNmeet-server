package dbmodels

import (
	"time"
)

type Recording struct {
	ID               int64     `gorm:"column:id;primaryKey;AUTO_INCREMENT"`
	RecordID         string    `gorm:"column:record_id;NOT NULL"`
	RoomID           string    `gorm:"column:room_id;NOT NULL"`
	RoomSid          string    `gorm:"column:room_sid"`
	RecorderID       string    `gorm:"column:recorder_id;NOT NULL"`
	FilePath         string    `gorm:"column:file_path;NOT NULL"`
	Size             float64   `gorm:"column:size;NOT NULL"`
	Published        int64     `gorm:"column:published;default:1;NOT NULL"`
	CreationTime     int64     `gorm:"column:creation_time;default:0;NOT NULL"`
	RoomCreationTime int64     `gorm:"column:room_creation_time;default:0;NOT NULL"`
	Created          time.Time `gorm:"column:created;default:CURRENT_TIMESTAMP;NOT NULL"`
	Modified         time.Time `gorm:"column:modified;default:0000-00-00 00:00:00;NOT NULL"`
}

func (m *Recording) TableName() string {
	return "pnm_recordings"
}
