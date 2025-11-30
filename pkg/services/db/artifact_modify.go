package dbservice

import (
	"errors"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
)

// CreateRoomArtifact inserts a new artifact record into the database.
// It returns the number of rows affected.
func (s *DatabaseService) CreateRoomArtifact(artifact *dbmodels.RoomArtifact) (int64, error) {
	result := s.db.Create(artifact)
	if result.Error != nil {
		return 0, result.Error
	}

	return result.RowsAffected, nil
}

// DeleteArtifactByArtifactId deletes an artifact by its unique artifact_id, enforcing business logic.
// It returns the number of rows affected.
func (s *DatabaseService) DeleteArtifactByArtifactId(artifactId string) (int64, error) {
	artifact, err := s.GetRoomArtifactByArtifactID(artifactId)
	if err != nil {
		return 0, err
	}
	if artifact == nil {
		// Record not found, so 0 rows affected.
		return 0, nil
	}

	//  prevent deletion of certain artifact types.
	if artifact.Type != plugnmeet.RoomArtifactType_MEETING_SUMMARY {
		return 0, errors.New("deleting this type of artifact is not allowed")
	}

	// If we get here, it's allowed.
	result := s.db.Delete(&artifact)
	if result.Error != nil {
		return 0, result.Error
	}

	return result.RowsAffected, nil
}
