package dbmodels

import "github.com/mynaparrot/plugnmeet-server/pkg/config"

type Analytics struct {
	ID               uint64  `gorm:"column:id;primaryKey;autoIncrement"`
	RoomTableID      uint64  `gorm:"column:room_table_id;unique"`
	RoomID           string  `gorm:"column:room_id;NOT NULL"`
	FileID           string  `gorm:"column:file_id;NOT NULL"`
	FileName         string  `gorm:"column:file_name;NOT NULL"`
	FileSize         float64 `gorm:"column:file_size;NOT NULL"`
	RoomCreationTime int64   `gorm:"column:room_creation_time;NOT NULL"`
	CreationTime     int64   `gorm:"column:creation_time;autoCreateTime;NOT NULL"`
}

func (m *Analytics) TableName() string {
	return config.FormatDBTable("room_analytics")
}
