package dbmodels

import (
	"database/sql"
	"time"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

type Recording struct {
	ID               uint64         `gorm:"column:id;type:int(11);primarykey;autoIncrement"`
	RecordID         string         `gorm:"column:record_id;type:varchar(64);not null;uniqueIndex:idx_record_id"`
	RoomID           string         `gorm:"column:room_id;type:varchar(64);not null;index:idx_room_id"`
	RoomSid          sql.NullString `gorm:"column:room_sid;type:varchar(64);not null"`
	RecorderID       string         `gorm:"column:recorder_id;type:varchar(36);not null"`
	FilePath         string         `gorm:"column:file_path;type:varchar(255);not null"`
	Size             string         `gorm:"column:size;type:double;not null"`
	Published        int64          `gorm:"column:published;type:int(1);not null;default:1"`
	Metadata         string         `gorm:"column:metadata;type:json"`
	CreationTime     int64          `gorm:"column:creation_time;type:int(10);not null;autoCreateTime"`
	RoomCreationTime int64          `gorm:"column:room_creation_time;type:int(10);not null;default:0"`
	Created          time.Time      `gorm:"column:created;type:datetime;not null;default:current_timestamp()"`
	Modified         time.Time      `gorm:"column:modified;type:datetime;not null;default:'0000-00-00 00:00:00';autoUpdateTime"`

	RoomInfo RoomInfo `gorm:"foreignKey:room_sid;references:sid;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`
}

func (t *Recording) TableName() string {
	return config.FormatDBTable("recordings")
}
