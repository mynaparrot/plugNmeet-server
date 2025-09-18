package models

import (
	"context"
	"fmt"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/sirupsen/logrus"
)

// EndRoom now accepts context and userIDForLog
func (m *RoomModel) EndRoom(ctx context.Context, r *plugnmeet.RoomEndReq) (bool, string) {
	roomID := r.GetRoomId()
	log := m.logger.WithField("roomId", roomID)

	if errWait := waitUntilRoomCreationCompletes(ctx, m.rs, roomID, m.logger); errWait != nil {
		log.WithError(errWait).Error("Cannot end room as it's locked")
		return false, fmt.Sprintf("Failed to end room: %s", errWait.Error())
	}
	log.Info("Proceeding to end room")

	roomDbInfo, err := m.ds.GetRoomInfoByRoomId(roomID, 1)
	if err != nil {
		return false, err.Error()
	}
	if roomDbInfo == nil || roomDbInfo.ID == 0 {
		return false, "room not found in DB or not active"
	}

	info, err := m.natsService.GetRoomInfo(roomID)
	if err != nil {
		log.WithError(err).Warn("NATS GetRoomInfo failed during EndRoom. Proceeding with DB cleanup.")
	}
	if info == nil && roomDbInfo.IsRunning == 1 {
		log.Warn("Room active in DB but not in NATS during EndRoom. Marking as ended and cleaning up.")
		go m.OnAfterRoomEnded(ctx, roomDbInfo.RoomId, roomDbInfo.Sid, "") // Metadata might be empty
		return true, "room ended (NATS info was missing, cleanup initiated)"
	}
	if info == nil {
		return false, "room not active (not found in NATS)"
	}
	// before cleanup, we'll hold room records temporary in redis
	// because room_finished event from LK may arrive delay and we can use it
	m.rs.HoldTemporaryRoomData(info)

	err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_SESSION_ENDED, roomID, "notifications.room-disconnected-room-ended", nil)
	if err != nil {
		log.WithError(err).Error("error sending session ended notification message")
	}

	if info.Status != natsservice.RoomStatusEnded {
		err = m.natsService.UpdateRoomStatus(roomID, natsservice.RoomStatusEnded)
		if err != nil {
			log.WithError(err).Error("error updating room status")
		}
		_, err = m.lk.EndRoom(roomID)
		if err != nil {
			log.WithError(err).Error("error ending room in livekit")
		}
	}
	go m.OnAfterRoomEnded(ctx, info.RoomId, info.RoomSid, info.Metadata)
	return true, "success"
}

func (m *RoomModel) OnAfterRoomEnded(ctx context.Context, roomID, roomSID, metadata string) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":    roomID,
		"roomSid":   roomSID,
		"operation": "OnAfterRoomEnded",
	})
	log.Info("Starting cleanup")

	cleanupLockTTL := config.WaitBeforeTriggerOnAfterRoomEnded + (time.Second * 10)
	lockAcquired, lockVal, errLock := m.rs.LockRoomCreation(ctx, roomID, cleanupLockTTL)

	if errLock != nil {
		log.WithError(errLock).Error("Redis error acquiring cleanup lock. Cleanup might be incomplete.")
	} else if !lockAcquired {
		log.Warn("Could not acquire cleanup lock. Cleanup might be incomplete.")
	}
	if lockAcquired {
		log.WithField("lockVal", lockVal).Info("Cleanup lock acquired")
		defer func() {
			unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := m.rs.UnlockRoomCreation(unlockCtx, roomID, lockVal); err != nil {
				log.WithField("lockVal", lockVal).WithError(err).Error("Error releasing cleanup lock")
			} else {
				log.WithField("lockVal", lockVal).Info("Cleanup lock released")
			}
		}()
	}

	_, err := m.ds.UpdateRoomStatus(&dbmodels.RoomInfo{RoomId: roomID, IsRunning: 0})
	if err != nil {
		log.WithError(err).Error("DB error updating status")
	}

	done := make(chan struct{})
	waitCtx, cancelWait := context.WithTimeout(ctx, 3*time.Second)
	defer cancelWait()
	go func() {
		for {
			select {
			case <-waitCtx.Done():
				log.WithField("subOperation", "userDisconnectWait").WithError(waitCtx.Err()).Warn("Wait cancelled/timed out")
				close(done)
				return
			default:
				if m.areAllUsersDisconnected(roomID) {
					log.WithField("subOperation", "userDisconnectWait").Info("All users disconnected")
					close(done)
					return
				}
				time.Sleep(1 * time.Second)
			}
		}
	}()
	select {
	case <-done:
		log.Info("Proceeding with cleanup after user disconnect")
	case <-waitCtx.Done():
		log.Warn("Timeout waiting for all users to disconnect (fallback)")
	}

	m.natsService.DeleteRoomUsersBlockList(roomID)

	if err = m.recorderModel.SendMsgToRecorder(&plugnmeet.RecordingReq{Task: plugnmeet.RecordingTasks_STOP, Sid: roomSID, RoomId: roomID}); err != nil {
		log.WithError(err).Error("Error sending stop to recorder")
	}

	if !m.app.UploadFileSettings.KeepForever {
		if err = m.fileModel.DeleteRoomUploadedDir(roomSID); err != nil {
			log.WithError(err).Error("Error deleting uploads")
		}
	}

	if err = m.roomDuration.DeleteRoomWithDuration(roomID); err != nil {
		log.WithError(err).Error("Error deleting room duration")
	}

	_ = m.etherpadModel.CleanAfterRoomEnd(roomID, metadata)

	if err = m.pollModel.CleanUpPolls(roomID); err != nil {
		log.WithError(err).Error("Error cleaning polls")
	}

	if err = m.breakoutModel.PostTaskAfterRoomEndWebhook(ctx, roomID, metadata); err != nil {
		log.WithError(err).Error("Error in breakout room post-end task")
	}

	if err = m.speechToText.OnAfterRoomEnded(roomID, roomSID); err != nil {
		log.WithError(err).Error("Error in speech service cleanup")
	}

	m.natsService.OnAfterSessionEndCleanup(roomID)
	log.Info("Room has been cleaned properly")

	m.analyticsModel.PrepareToExportAnalytics(roomID, roomSID, metadata)
}

// Helper function to check if all users are disconnected
func (m *RoomModel) areAllUsersDisconnected(roomId string) bool {
	users, err := m.natsService.GetOnlineUsersId(roomId)
	if err != nil || users == nil || len(users) == 0 {
		return true
	}
	return false
}
