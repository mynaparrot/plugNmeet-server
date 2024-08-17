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

func (s *DatabaseService) GetAnalytics(roomIds []string, offset, limit uint64, direction *string) ([]dbmodels.Analytics, int64, error) {
	var analytics []dbmodels.Analytics

	d := s.db.Model(&dbmodels.Analytics{})
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

	result := d.Offset(int(offset)).Limit(int(limit)).Order("id " + orderBy).Find(&analytics)
	switch {
	case errors.Is(result.Error, gorm.ErrRecordNotFound):
		return nil, 0, nil
	case result.Error != nil:
		return nil, 0, result.Error
	}

	var total int64
	if len(analytics) > 0 {
		d = s.db.Model(&dbmodels.Analytics{})
		if len(roomIds) > 0 {
			d.Where("room_id IN ?", roomIds)
		}
		d.Count(&total)
	}

	return analytics, total, nil
}

func (s *DatabaseService) GetAnalyticByFileId(fileId string) (*dbmodels.Analytics, error) {
	info := new(dbmodels.Analytics)
	cond := &dbmodels.Analytics{
		FileID: fileId,
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

func (s *DatabaseService) GetAnalyticByRoomTableId(roomTableId uint64) (*dbmodels.Analytics, error) {
	info := new(dbmodels.Analytics)
	cond := &dbmodels.Analytics{
		RoomTableID: roomTableId,
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
