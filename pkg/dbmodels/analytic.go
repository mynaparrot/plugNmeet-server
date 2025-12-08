package dbmodels

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

type Analytics struct {
	ID               uint64  `gorm:"column:id;type:int(11);primarykey;autoIncrement"`
	RoomTableID      uint64  `gorm:"column:room_table_id;type:int(11);not null;uniqueIndex:idx_room_table_id"`
	RoomID           string  `gorm:"column:room_id;type:varchar(64);not null;index:idx_room_id"`
	FileID           string  `gorm:"column:file_id;type:varchar(255);not null;uniqueIndex:idx_file_id"`
	FileName         string  `gorm:"column:file_name;type:varchar(255);not null"`
	FileSize         float64 `gorm:"column:file_size;type:double;not null"`
	RoomCreationTime int64   `gorm:"column:room_creation_time;type:int(11);not null"`
	CreationTime     int64   `gorm:"column:creation_time;type:int(11);autoCreateTime;not null"`

	RoomInfo RoomInfo `gorm:"foreignKey:room_table_id;references:id;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`
}

func (t *Analytics) TableName() string {
	return config.FormatDBTable("room_analytics")
}
