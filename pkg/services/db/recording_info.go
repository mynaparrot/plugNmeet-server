package dbservice

import (
	"errors"

	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"gorm.io/gorm"
)

func (s *DatabaseService) GetRecordings(roomIds []string, offset, limit uint64, direction *string) ([]dbmodels.Recording, int64, error) {
	var recordings []dbmodels.Recording
	var total int64

	d := s.db.Model(&dbmodels.Recording{})
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

	result := d.Offset(int(offset)).Limit(int(limit)).Find(&recordings)
	if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, 0, result.Error
	}

	return recordings, total, nil
}
