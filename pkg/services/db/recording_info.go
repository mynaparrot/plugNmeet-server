package dbservice

import (
	"errors"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"gorm.io/gorm"
)

func (s *DatabaseService) GetRecordings(roomIds []string, roomSid *string, offset, limit uint64, direction *string) ([]dbmodels.Recording, int64, error) {
	var recordings []dbmodels.Recording
	var total int64

	d := s.db.Model(&dbmodels.Recording{})

	if roomSid != nil {
		d.Where("room_sid = ?", *roomSid)
	} else if len(roomIds) > 0 {
		d.Where("room_id IN ?", roomIds)
	}

	if err := d.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if total == 0 {
		return recordings, 0, nil
	}

	if limit == 0 {
		limit = 20
	}

	orderBy := "DESC"
	if direction != nil && *direction == "ASC" {
		orderBy = "ASC"
	}

	result := d.Offset(int(offset)).Limit(int(limit)).Order("id " + orderBy).Find(&recordings)
	if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, 0, result.Error
	}

	return recordings, total, nil
}

func (s *DatabaseService) GetRecording(recordId string) (*dbmodels.Recording, error) {
	info := new(dbmodels.Recording)
	cond := &dbmodels.Recording{
		RecordID: recordId,
	}

	result := s.db.Where(cond).Take(info)
	switch {
	case errors.Is(result.Error, gorm.ErrRecordNotFound):
		return nil, nil
	case result.Error != nil:
		return nil, result.Error
	}

	return info, nil
}

func (s *DatabaseService) GetRecordingsForBBB(recordIds, meetingIds []string, offset, limit uint64) ([]dbmodels.Recording, int64, error) {
	var recordings []dbmodels.Recording
	var total int64

	d := s.db.Model(&dbmodels.Recording{})

	if len(recordIds) > 0 {
		d.Where("record_id IN ?", recordIds)
	} else if len(meetingIds) > 0 {
		d.Where("room_id IN ?", meetingIds)
	}

	if err := d.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	result := d.Offset(int(offset)).Limit(int(limit)).Order("id DESC").Find(&recordings)
	if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, 0, result.Error
	}

	return recordings, total, nil
}

func (s *DatabaseService) GetRecordingsByIDs(recordIDs []string, roomID string) ([]dbmodels.Recording, error) {
	var recordingsFromDB []dbmodels.Recording

	// Fetch all recordings in one query, filtering by both record IDs and room ID.
	if err := s.db.Model(&dbmodels.Recording{}).Where("room_id = ? AND record_id IN ?", roomID, recordIDs).Find(&recordingsFromDB).Error; err != nil {
		return nil, err
	}

	// Ensure all requested recordings were found for that room.
	if len(recordingsFromDB) != len(recordIDs) {
		// This indicates that either some recordIDs did not exist, or they did not belong to the specified roomID.
		return nil, config.ErrRequestedRecordingsNotFound
	}

	// Create a map for quick lookups to preserve the original order.
	recordMap := make(map[string]dbmodels.Recording)
	for _, rec := range recordingsFromDB {
		recordMap[rec.RecordID] = rec
	}

	// Re-order the results to match the input order.
	var orderedRecordings []dbmodels.Recording
	for _, id := range recordIDs {
		rec, found := recordMap[id]
		if !found {
			// This should not happen due to the length check above, but it's a safeguard.
			return nil, config.ErrRequestedRecordingsNotFound
		}
		orderedRecordings = append(orderedRecordings, rec)
	}

	return orderedRecordings, nil
}
