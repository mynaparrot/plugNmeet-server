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

// GetAllAnalyticsFiles retrieves all records from the pnm_room_analytics table for migration.
func (s *DatabaseService) GetAllAnalyticsFiles() ([]*dbmodels.Analytics, error) {
	var analytics []*dbmodels.Analytics
	result := s.db.Find(&analytics)
	if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, result.Error
	}
	return analytics, nil
}
