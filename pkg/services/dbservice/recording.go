package dbservice

import (
	"errors"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"gorm.io/gorm"
)

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
