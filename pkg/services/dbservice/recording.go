package dbservice

import (
	"errors"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"gorm.io/gorm"
)

func (s *DatabaseService) InsertRecordingData(info *dbmodels.Recording) (int64, error) {
	result := s.db.Create(info)
	if result.Error != nil {
		return 0, result.Error
	}

	return result.RowsAffected, nil
}

func (s *DatabaseService) GetRecordings(roomIds []string, offset, limit uint64, direction *string) ([]dbmodels.Recording, int64, error) {
	var recordings []dbmodels.Recording

	d := s.db.Model(&dbmodels.Recording{})
	if len(roomIds) > 0 {
		d.Where("room_id IN ?", roomIds)
	}

	if limit == 0 {
		limit = 20
	}

	orderBy := "DESC"
	if direction != nil && *direction == "ASC" {
		orderBy = "ASC"
	}

	result := d.Offset(int(offset)).Limit(int(limit)).Order("id " + orderBy).Find(&recordings)
	switch {
	case errors.Is(result.Error, gorm.ErrRecordNotFound):
		return nil, 0, nil
	case result.Error != nil:
		return nil, 0, result.Error
	}

	var total int64
	if len(recordings) > 0 {
		d = s.db.Model(&dbmodels.Recording{})
		if len(roomIds) > 0 {
			d.Where("room_id IN ?", roomIds)
		}
		d.Count(&total)
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

func (s *DatabaseService) DeleteRecording(recordId string) (int64, error) {
	cond := &dbmodels.Recording{
		RecordID: recordId,
	}

	result := s.db.Where(cond).Delete(&dbmodels.Recording{})
	switch {
	case errors.Is(result.Error, gorm.ErrRecordNotFound):
		return 0, nil
	case result.Error != nil:
		return 0, result.Error
	}

	return result.RowsAffected, nil
}
