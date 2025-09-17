package dbservice

import (
	"errors"

	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"gorm.io/gorm"
)

func (s *DatabaseService) InsertAnalyticsData(info *dbmodels.Analytics) (int64, error) {
	result := s.db.Create(info)
	if result.Error != nil {
		return 0, result.Error
	}

	return result.RowsAffected, nil
}

func (s *DatabaseService) DeleteAnalyticByFileId(fileId string) (int64, error) {
	cond := &dbmodels.Analytics{
		FileID: fileId,
	}

	result := s.db.Where(cond).Delete(&dbmodels.Analytics{})
	switch {
	case errors.Is(result.Error, gorm.ErrRecordNotFound):
		return 0, nil
	case result.Error != nil:
		return 0, result.Error
	}

	return result.RowsAffected, nil
}
