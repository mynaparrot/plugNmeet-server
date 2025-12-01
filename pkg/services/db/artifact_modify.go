package dbservice

import (
	"fmt"

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
		return 0, nil
	}

	// double check to prevent deletion of certain artifact types.
	if !s.IsAllowToDeleteArtifact(artifact.Type) {
		return 0, fmt.Errorf("deleting '%s' type of artifact is not allowed", artifact.Type)
	}

	// If we get here, it's allowed.
	result := s.db.Delete(&artifact)
	if result.Error != nil {
		return 0, result.Error
	}

	return result.RowsAffected, nil
}
func (s *DatabaseService) IsAllowToDeleteArtifact(artifactType plugnmeet.RoomArtifactType) bool {
	switch artifactType {
	case plugnmeet.RoomArtifactType_MEETING_SUMMARY:
		return true
	}

	return false
}
