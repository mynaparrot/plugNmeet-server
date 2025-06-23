package models

import (
	"context"
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	log "github.com/sirupsen/logrus"
	"time"
)

// EndRoom now accepts context and userIDForLog
func (m *RoomModel) EndRoom(ctx context.Context, r *plugnmeet.RoomEndReq) (bool, string) {
	roomID := r.GetRoomId()
	if errWait := waitUntilRoomCreationCompletes(ctx, m.rs, roomID); errWait != nil {
		log.WithFields(log.Fields{"roomId": roomID}).Errorf("Cannot end room: %v", errWait)
		return false, fmt.Sprintf("Failed to end room: %s", errWait.Error())
	}
	log.WithFields(log.Fields{"roomId": roomID}).Info("Proceeding to end room.")

	roomDbInfo, err := m.ds.GetRoomInfoByRoomId(roomID, 1)
	if err != nil {
		return false, err.Error()
	}
	if roomDbInfo == nil || roomDbInfo.ID == 0 {
		return false, "room not found in DB or not active"
	}

	info, err := m.natsService.GetRoomInfo(roomID)
	if err != nil {
		log.WithFields(log.Fields{"roomId": roomID}).Warnf("NATS GetRoomInfo failed during EndRoom: %v. Proceeding with DB cleanup.", err)
	}
	if info == nil && roomDbInfo.IsRunning == 1 {
		log.WithFields(log.Fields{"roomId": roomID}).Warn("Room active in DB but not in NATS during EndRoom. Marking as ended and cleaning up.")
		go m.OnAfterRoomEnded(ctx, roomDbInfo.RoomId, roomDbInfo.Sid, "") // Metadata might be empty
		return true, "room ended (NATS info was missing, cleanup initiated)"
	}
	if info == nil {
		return false, "room not active (not found in NATS)"
	}

	err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_SESSION_ENDED, roomID, "notifications.room-disconnected-room-ended", nil)
	if err != nil {
		log.Errorln(err)
	}

	if info.Status != natsservice.RoomStatusEnded {
		err = m.natsService.UpdateRoomStatus(roomID, natsservice.RoomStatusEnded)
		if err != nil {
			log.Errorln(err)
		}
		_, err = m.lk.EndRoom(roomID)
		if err != nil {
			log.Errorln(err)
		}
	}
	go m.OnAfterRoomEnded(ctx, info.RoomId, info.RoomSid, info.Metadata)
	return true, "success"
}

func (m *RoomModel) OnAfterRoomEnded(ctx context.Context, roomID, roomSID, metadata string) {
	log.WithFields(log.Fields{"roomId": roomID, "roomSid": roomSID, "operation": "OnAfterRoomEnded"}).Info("Starting cleanup.")

	cleanupLockTTL := config.WaitBeforeTriggerOnAfterRoomEnded + (time.Second * 10)
	lockAcquired, lockVal, errLock := m.rs.LockRoomCreation(ctx, roomID, cleanupLockTTL)

	if errLock != nil {
		log.WithFields(log.Fields{"roomId": roomID, "operation": "OnAfterRoomEnded"}).Errorf("Redis error acquiring cleanup lock: %v. Cleanup might be incomplete.", errLock)
	} else if !lockAcquired {
		log.WithFields(log.Fields{"roomId": roomID, "operation": "OnAfterRoomEnded"}).Warn("Could not acquire cleanup lock. Cleanup might be incomplete.")
	}
	if lockAcquired {
		log.WithFields(log.Fields{"roomId": roomID, "lockVal": lockVal, "operation": "OnAfterRoomEnded"}).Info("Cleanup lock acquired.")
		defer func() {
			unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := m.rs.UnlockRoomCreation(unlockCtx, roomID, lockVal); err != nil {
				log.WithFields(log.Fields{"roomId": roomID, "lockVal": lockVal, "operation": "OnAfterRoomEnded"}).Errorf("Error releasing cleanup lock: %v", err)
			} else {
				log.WithFields(log.Fields{"roomId": roomID, "lockVal": lockVal, "operation": "OnAfterRoomEnded"}).Info("Cleanup lock released.")
			}
		}()
	}

	_, err := m.ds.UpdateRoomStatus(&dbmodels.RoomInfo{RoomId: roomID, IsRunning: 0})
	if err != nil {
		log.WithFields(log.Fields{"roomId": roomID, "operation": "OnAfterRoomEnded"}).Errorf("DB error updating status: %v", err)
	}

	done := make(chan struct{})
	waitCtx, cancelWait := context.WithTimeout(ctx, 3*time.Second)
	defer cancelWait()
	go func() {
		for {
			select {
			case <-waitCtx.Done():
				log.WithFields(log.Fields{"roomId": roomID, "operation": "OnAfterRoomEnded.userDisconnectWait"}).Warnf("Wait cancelled/timed out: %v", waitCtx.Err())
				close(done)
				return
			default:
				if m.areAllUsersDisconnected(roomID) {
					log.WithFields(log.Fields{"roomId": roomID, "operation": "OnAfterRoomEnded.userDisconnectWait"}).Info("All users disconnected.")
					close(done)
					return
				}
				time.Sleep(1 * time.Second)
			}
		}
	}()
	select {
	case <-done:
		log.WithFields(log.Fields{"roomId": roomID, "operation": "OnAfterRoomEnded"}).Info("Proceeding with cleanup after user disconnect.")
	case <-waitCtx.Done():
		log.WithFields(log.Fields{"roomId": roomID, "operation": "OnAfterRoomEnded"}).Warn("Timeout waiting for all users to disconnect (fallback).")
	}

	m.natsService.DeleteRoomUsersBlockList(roomID)

	recorderModel := NewRecorderModel(m.app, m.ds, m.rs)
	if err = recorderModel.SendMsgToRecorder(&plugnmeet.RecordingReq{Task: plugnmeet.RecordingTasks_STOP, Sid: roomSID, RoomId: roomID}); err != nil {
		log.WithFields(log.Fields{"roomId": roomID, "roomSid": roomSID}).Errorf("Error sending stop to recorder: %v", err)
	}

	if !m.app.UploadFileSettings.KeepForever {
		fileM := NewFileModel(m.app, m.ds, m.natsService)
		if err = fileM.DeleteRoomUploadedDir(roomSID); err != nil {
			log.WithFields(log.Fields{"roomId": roomID, "roomSid": roomSID}).Errorf("Error deleting uploads: %v", err)
		}
	}

	rmDuration := NewRoomDurationModel(m.app, m.rs)
	if err = rmDuration.DeleteRoomWithDuration(roomID); err != nil {
		log.WithFields(log.Fields{"roomId": roomID}).Errorf("Error deleting room duration: %v", err)
	}

	em := NewEtherpadModel(m.app, m.ds, m.rs)
	_ = em.CleanAfterRoomEnd(roomID, metadata)

	pm := NewPollModel(m.app, m.ds, m.rs)
	if err = pm.CleanUpPolls(roomID); err != nil {
		log.WithFields(log.Fields{"roomId": roomID}).Errorf("Error cleaning polls: %v", err)
	}

	bm := NewBreakoutRoomModel(m.app, m.ds, m.rs)
	if err = bm.PostTaskAfterRoomEndWebhook(ctx, roomID, metadata); err != nil {
		log.WithFields(log.Fields{"roomId": roomID}).Errorf("Error in breakout room post-end task: %v", err)
	}

	sm := NewSpeechToTextModel(m.app, m.ds, m.rs)
	if err = sm.OnAfterRoomEnded(roomID, roomSID); err != nil {
		log.WithFields(log.Fields{"roomId": roomID, "roomSid": roomSID}).Errorf("Error in speech service cleanup: %v", err)
	}

	m.natsService.OnAfterSessionEndCleanup(roomID)
	log.WithFields(log.Fields{"roomId": roomID, "operation": "OnAfterRoomEnded"}).Info("Room has been cleaned properly.")

	analyticsModel := NewAnalyticsModel(m.app, m.ds, m.rs)
	analyticsModel.PrepareToExportAnalytics(roomID, roomSID, metadata)
}

// Helper function to check if all users are disconnected
func (m *RoomModel) areAllUsersDisconnected(roomId string) bool {
	users, err := m.natsService.GetOnlineUsersId(roomId)
	if err != nil || users == nil || len(users) == 0 {
		return true
	}
	return false
}
