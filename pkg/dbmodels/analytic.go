package dbmodels

type Analytics struct {
	ID               int64   `gorm:"column:id;primaryKey;AUTO_INCREMENT"`
	RoomTableID      int64   `gorm:"column:room_table_id"`
	RoomID           string  `gorm:"column:room_id;NOT NULL"`
	FileID           string  `gorm:"column:file_id;NOT NULL"`
	FileName         string  `gorm:"column:file_name;NOT NULL"`
	FileSize         float64 `gorm:"column:file_size;NOT NULL"`
	RoomCreationTime int64   `gorm:"column:room_creation_time;NOT NULL"`
	CreationTime     int64   `gorm:"column:creation_time;NOT NULL"`
}

func (m *Analytics) TableName() string {
	return "pnm_room_analytics"
}