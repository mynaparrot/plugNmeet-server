package dbservice

import (
	"errors"

	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"gorm.io/gorm"
)

// GetArtifacts retrieves a paginated and sorted list of artifacts,
// optionally filtered by room IDs, and returns the total count.
func (s *DatabaseService) GetArtifacts(roomIds []string, offset, limit uint64, direction *string) ([]*dbmodels.RoomArtifact, int64, error) {
	var artifacts []*dbmodels.RoomArtifact
	var total int64

	tx := s.db.Model(&dbmodels.RoomArtifact{})

	if len(roomIds) > 0 {
		tx = tx.Where("room_id IN ?", roomIds)
	}

	// Get the total count before applying limit and offset
	err := tx.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	if direction != nil && (*direction == "ASC" || *direction == "DESC") {
		tx = tx.Order("creation_time " + *direction)
	} else {
		tx = tx.Order("creation_time DESC")
	}

	if limit > 0 {
		tx = tx.Limit(int(limit))
	}
	if offset > 0 {
		tx = tx.Offset(int(offset))
	}

	err = tx.Find(&artifacts).Error
	if err != nil {
		return nil, 0, err
	}

	return artifacts, total, nil
}

// GetRoomArtifactByArtifactID retrieves a single artifact by its unique artifact_id.
// It returns (nil, nil) if the record is not found.
func (s *DatabaseService) GetRoomArtifactByArtifactID(artifactID string) (*dbmodels.RoomArtifact, error) {
	var artifact dbmodels.RoomArtifact
	result := s.db.Where("artifact_id = ?", artifactID).First(&artifact)

	switch {
	case errors.Is(result.Error, gorm.ErrRecordNotFound):
		return nil, nil
	case result.Error != nil:
		return nil, result.Error
	}

	return &artifact, nil
}
