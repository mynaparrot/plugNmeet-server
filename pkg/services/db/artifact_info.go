package dbservice

import (
	"errors"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"gorm.io/gorm"
)

// GetArtifacts retrieves a paginated and sorted list of artifacts,
// optionally filtered by room IDs, roomSid and artifact type, and returns the total count.
func (s *DatabaseService) GetArtifacts(roomIds []string, roomSid *string, artifactType *plugnmeet.RoomArtifactType, offset, limit uint64, direction *string) ([]*dbmodels.RoomArtifact, int64, error) {
	var artifacts []*dbmodels.RoomArtifact
	var total int64

	tx := s.db.Model(&dbmodels.RoomArtifact{})

	if roomSid != nil {
		// Use a subquery to avoid the N+1 problem
		subQuery := s.db.Model(&dbmodels.RoomInfo{}).Select("id").Where("sid = ?", *roomSid)
		tx.Where("room_table_id = (?)", subQuery)
	} else if len(roomIds) > 0 {
		tx.Where("room_id IN ?", roomIds)
	}

	if artifactType != nil {
		// Convert the enum to its string name for the query
		tx.Where("type = ?", artifactType.String())
	}

	// Get the total count before applying limit and offset
	err := tx.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	if total == 0 {
		return artifacts, 0, nil
	}

	if limit == 0 {
		limit = 20
	}

	orderBy := "DESC"
	if direction != nil && *direction == "ASC" {
		orderBy = "ASC"
	}

	result := tx.Offset(int(offset)).Limit(int(limit)).Order("id " + orderBy).Find(&artifacts)
	if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, 0, result.Error
	}

	return artifacts, total, nil
}

// GetRoomArtifactByArtifactID retrieves a single artifact by its unique artifact_id.
// It returns (nil, nil) if the record is not found.
func (s *DatabaseService) GetRoomArtifactByArtifactID(artifactID string) (*dbmodels.RoomArtifact, error) {
	var artifact dbmodels.RoomArtifact
	cond := &dbmodels.RoomArtifact{
		ArtifactId: artifactID,
	}
	result := s.db.Where(cond).First(&artifact)

	switch {
	case errors.Is(result.Error, gorm.ErrRecordNotFound):
		return nil, nil
	case result.Error != nil:
		return nil, result.Error
	}

	return &artifact, nil
}

// GetRoomArtifactDetails retrieves a single artifact by its unique artifact_id,
// preloading the associated RoomInfo.
// It returns config.NotFoundErr if the record is not found.
func (s *DatabaseService) GetRoomArtifactDetails(artifactID string) (*dbmodels.RoomArtifact, *dbmodels.RoomInfo, error) {
	var artifact dbmodels.RoomArtifact
	cond := &dbmodels.RoomArtifact{
		ArtifactId: artifactID,
	}
	result := s.db.Preload("RoomInfo").Where(cond).First(&artifact)

	switch {
	case errors.Is(result.Error, gorm.ErrRecordNotFound):
		return nil, nil, config.NotFoundErr
	case result.Error != nil:
		return nil, nil, result.Error
	}

	return &artifact, &artifact.RoomInfo, nil
}

func (s *DatabaseService) GetAnalyticByRoomTableId(roomTableId uint64) (*dbmodels.RoomArtifact, error) {
	artifact := new(dbmodels.RoomArtifact)
	cond := &dbmodels.RoomArtifact{
		RoomTableID: roomTableId,
		Type:        dbmodels.RoomArtifactType(plugnmeet.RoomArtifactType_MEETING_ANALYTICS),
	}

	result := s.db.Where(cond).Take(artifact)
	switch {
	case errors.Is(result.Error, gorm.ErrRecordNotFound):
		return nil, nil
	case result.Error != nil:
		return nil, result.Error
	}

	return artifact, nil
}
