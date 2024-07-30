package dbservice

import (
	"errors"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"gorm.io/gorm"
	"time"
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

	result := s.db.Where("sid = ? OR sid = CONCAT(?, '-', id)", sId, sId).Where(cond).Take(info)
	switch {
	case errors.Is(result.Error, gorm.ErrRecordNotFound):
		return nil, nil
	case result.Error != nil:
		return nil, result.Error
	}

	return info, nil
}

func (s *DatabaseService) GetRoomInfoByTableId(tableId int64) (*dbmodels.RoomInfo, error) {
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

// InsertOrUpdateRoomInfo will insert if sid do not duplicate
// otherwise it will update if table ID was sent
func (s *DatabaseService) InsertOrUpdateRoomInfo(info *dbmodels.RoomInfo) (int64, error) {
	result := s.db.Save(info)
	if result.Error != nil {
		return 0, result.Error
	}

	return result.RowsAffected, nil
}

func (s *DatabaseService) UpdateRoomStatus(info *dbmodels.RoomInfo) (int64, error) {
	update := map[string]interface{}{
		"is_running": info.IsRunning,
	}

	if info.IsRunning == 0 {
		update["sid"] = gorm.Expr("CONCAT(sid, '-', id)")
		update["is_recording"] = 0
		update["is_active_rtmp"] = 0
		update["ended"] = time.Now().UTC().Format("2006-01-02 15:04:05")
	}

	cond := &dbmodels.RoomInfo{}
	if info.RoomId != "" {
		cond.RoomId = info.RoomId
	} else {
		cond.Sid = info.Sid
	}

	result := s.db.Model(&dbmodels.RoomInfo{}).Where(cond).Not("is_running = ?", info.IsRunning).Updates(update)
	if result.Error != nil {
		return 0, result.Error
	}

	return result.RowsAffected, nil
}
