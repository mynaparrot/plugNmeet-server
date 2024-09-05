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
	d := s.db.Model(&dbmodels.Recording{})

	if len(recordIds) > 0 {
		d.Where("record_id IN ?", recordIds)
	} else if len(meetingIds) > 0 {
		d.Where("room_id IN ?", meetingIds)
	}

	result := d.Offset(int(offset)).Limit(int(limit)).Find(&recordings)
	switch {
	case errors.Is(result.Error, gorm.ErrRecordNotFound):
		return nil, 0, nil
	case result.Error != nil:
		return nil, 0, result.Error
	}

	var total int64
	if len(recordings) > 0 {
		d = s.db.Model(&dbmodels.Recording{})
		if len(recordIds) > 0 {
			d.Where("record_id IN ?", recordIds)
		} else if len(meetingIds) > 0 {
			d.Where("room_id IN ?", meetingIds)
		}
		d.Count(&total)
	}

	return recordings, total, nil
}
