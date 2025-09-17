package dbservice

import (
	"errors"

	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"gorm.io/gorm"
)

func (s *DatabaseService) GetRoomInfoByRoomId(roomId string, isRunning int) (*dbmodels.RoomInfo, error) {
	info := new(dbmodels.RoomInfo)
	cond := &dbmodels.RoomInfo{
		RoomId:    roomId,
		IsRunning: isRunning,
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

func (s *DatabaseService) GetRoomInfoBySid(sId string, isRunning *int) (*dbmodels.RoomInfo, error) {
	info := new(dbmodels.RoomInfo)
	cond := &dbmodels.RoomInfo{}
	if isRunning != nil {
		cond.IsRunning = *isRunning
	}

	result := s.db.Where("sid = ?", sId).Where(cond).Take(info)
	switch {
	case errors.Is(result.Error, gorm.ErrRecordNotFound):
		return nil, nil
	case result.Error != nil:
		return nil, result.Error
	}

	return info, nil
}

func (s *DatabaseService) GetRoomInfoByTableId(tableId uint64) (*dbmodels.RoomInfo, error) {
	info := new(dbmodels.RoomInfo)
	cond := &dbmodels.RoomInfo{
		ID: tableId,
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

func (s *DatabaseService) GetActiveRoomsInfo() ([]dbmodels.RoomInfo, error) {
	var rooms []dbmodels.RoomInfo
	cond := &dbmodels.RoomInfo{
		IsRunning: 1,
	}

	result := s.db.Where(cond).Find(&rooms)
	switch {
	case errors.Is(result.Error, gorm.ErrRecordNotFound):
		return nil, nil
	case result.Error != nil:
		return nil, result.Error
	}

	return rooms, nil
}

func (s *DatabaseService) GetPastRooms(roomIds []string, offset, limit uint64, direction *string) ([]dbmodels.RoomInfo, int64, error) {
	var roomsInfo []dbmodels.RoomInfo
	var total int64
	cond := &dbmodels.RoomInfo{
		IsRunning: 0,
	}

	d := s.db.Model(&dbmodels.RoomInfo{}).Where(cond)
	if len(roomIds) > 0 {
		d.Where("roomId IN ?", roomIds)
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

	result := d.Offset(int(offset)).Limit(int(limit)).Order("id " + orderBy).Find(&roomsInfo)
	if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, 0, result.Error
	}

	return roomsInfo, total, nil
}
