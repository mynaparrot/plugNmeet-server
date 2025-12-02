package dbmodels

import (
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

type RoomArtifact struct {
	ID          uint64                     `gorm:"primarykey"`
	ArtifactId  string                     `gorm:"column:artifact_id;not null;uniqueIndex"`
	RoomTableID uint64                     `gorm:"column:room_table_id;not null;index"`
	RoomId      string                     `gorm:"column:room_id;not null;index"`
	Type        plugnmeet.RoomArtifactType `gorm:"column:type;not null;index"`
	Metadata    string                     `gorm:"column:metadata;type:json"`
	Created     time.Time                  `gorm:"column:created;not null;autoCreateTime"`
}

func (t *RoomArtifact) TableName() string {
	return config.FormatDBTable("room_artifacts")
}
