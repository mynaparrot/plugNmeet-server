package dbmodels

import (
	"database/sql/driver"
	"fmt"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

type RoomArtifactType plugnmeet.RoomArtifactType

// Value implements the driver.Valuer interface.
// This method is called when writing to the database.
func (t RoomArtifactType) Value() (driver.Value, error) {
	// Convert the enum integer to its string representation.
	return plugnmeet.RoomArtifactType_name[int32(t)], nil
}

func (t RoomArtifactType) ToString() string {
	return plugnmeet.RoomArtifactType_name[int32(t)]
}

// Scan implements the sql.Scanner interface.
// This method is called when reading from the database.
func (t *RoomArtifactType) Scan(value interface{}) error {
	// The value from the database will be a []byte (raw string).
	b, ok := value.([]byte)
	if !ok {
		return fmt.Errorf("failed to scan RoomArtifactType: value is not []byte")
	}
	// Convert the string name back to the enum's integer value.
	*t = RoomArtifactType(plugnmeet.RoomArtifactType_value[string(b)])
	return nil
}

type RoomArtifact struct {
	ID          uint64           `gorm:"primarykey"`
	ArtifactId  string           `gorm:"column:artifact_id;not null;uniqueIndex"`
	RoomTableID uint64           `gorm:"column:room_table_id;not null;index"`
	RoomId      string           `gorm:"column:room_id;not null;index"`
	Type        RoomArtifactType `gorm:"column:type;not null;index"`
	Metadata    string           `gorm:"column:metadata;type:json"`
	Created     time.Time        `gorm:"column:created;not null;autoCreateTime"`
}

func (t *RoomArtifact) TableName() string {
	return config.FormatDBTable("room_artifacts")
}
