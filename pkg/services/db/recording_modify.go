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
