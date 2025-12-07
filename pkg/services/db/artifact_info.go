package dbservice

import (
	"errors"
	"fmt"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
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
		isRunning := 0
		roomInfo, err := s.GetRoomInfoBySid(*roomSid, &isRunning)
		if err != nil {
			return nil, 0, err
		}
		if roomInfo == nil {
			return nil, 0, fmt.Errorf("room not found with sid: %s", *roomSid)
		}
		tx.Where("room_table_id = ?", roomInfo.ID)
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

func (s *DatabaseService) GetRoomArtifactDetails(artifactID string) (*dbmodels.RoomArtifact, *dbmodels.RoomInfo, error) {
	artifact, err := s.GetRoomArtifactByArtifactID(artifactID)
	if err != nil {
		return nil, nil, err
	}
	if artifact == nil {
		return nil, nil, fmt.Errorf("artifact not found with ID: %s", artifactID)
	}

	roomInfo, err := s.GetRoomInfoByTableId(artifact.RoomTableID)
	if err != nil {
		// it should not happen but we're fine
	}

	return artifact, roomInfo, nil
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
