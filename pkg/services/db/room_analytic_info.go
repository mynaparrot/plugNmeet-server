package dbservice

import (
	"errors"

	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"gorm.io/gorm"
)

func (s *DatabaseService) GetAnalytics(roomIds []string, offset, limit uint64, direction *string) ([]dbmodels.Analytics, int64, error) {
	var analytics []dbmodels.Analytics
	var total int64

	d := s.db.Model(&dbmodels.Analytics{})
	if len(roomIds) > 0 {
		d.Where("room_id IN ?", roomIds)
	}

	if err := d.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if limit == 0 {
		limit = 20
	}

	orderBy := "DESC"
	if direction != nil && *direction == "ASC" {
		orderBy = "ASC"
	}

	result := d.Offset(int(offset)).Limit(int(limit)).Order("id " + orderBy).Find(&analytics)
	if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, 0, result.Error
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
