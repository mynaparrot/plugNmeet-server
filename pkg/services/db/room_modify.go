package dbservice

import (
	"time"

	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"gorm.io/gorm"
)

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
		update["is_recording"] = 0
		update["is_active_rtmp"] = 0
		update["ended"] = time.Now()
	}

	cond := new(dbmodels.RoomInfo)
	if info.ID > 0 {
		cond.ID = info.ID
	} else if info.RoomId != "" {
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

func (s *DatabaseService) UpdateRoomRecordingStatus(roomTableId uint64, isRecording int, recorderId *string) (int64, error) {
	cond := &dbmodels.RoomInfo{
		ID: roomTableId,
	}

	update := map[string]interface{}{
		"is_recording": isRecording,
	}
	if recorderId != nil && *recorderId != "" {
		update["recorder_id"] = *recorderId
	}

	result := s.db.Model(&dbmodels.RoomInfo{}).Where(cond).Updates(update)
	if result.Error != nil {
		return 0, result.Error
	}

	return result.RowsAffected, nil
}

func (s *DatabaseService) UpdateRoomRTMPStatus(roomTableId uint64, isActiveRtmp int, rtmpNodeId *string) (int64, error) {
	cond := &dbmodels.RoomInfo{
		ID: roomTableId,
	}

	update := map[string]interface{}{
		"is_active_rtmp": isActiveRtmp,
	}
	if rtmpNodeId != nil && *rtmpNodeId != "" {
		update["rtmp_node_id"] = *rtmpNodeId
	}

	result := s.db.Model(&dbmodels.RoomInfo{}).Where(cond).Updates(update)
	if result.Error != nil {
		return 0, result.Error
	}

	return result.RowsAffected, nil
}

func (s *DatabaseService) UpdateNumParticipants(sId string, num int64) (int64, error) {
	update := map[string]interface{}{
		"joined_participants": num,
	}

	result := s.db.Model(&dbmodels.RoomInfo{}).Where("sid = ?", sId).Updates(update)
	if result.Error != nil {
		return 0, result.Error
	}

	return result.RowsAffected, nil
}

// IncrementOrDecrementNumParticipants will increment or decrement the number of Participants
func (s *DatabaseService) IncrementOrDecrementNumParticipants(sId, operator string) (int64, error) {
	update := map[string]interface{}{
		"joined_participants": gorm.Expr("GREATEST(CAST(joined_participants AS SIGNED)" + operator + " 1, 0)"),
	}

	result := s.db.Model(&dbmodels.RoomInfo{}).Where("sid = ?", sId).Updates(update)
	if result.Error != nil {
		return 0, result.Error
	}

	return result.RowsAffected, nil
}
