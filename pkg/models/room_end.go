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
	log := m.logger.WithFields(logrus.Fields{
		"room_id": roomID,
		"method":  "EndRoom",
	})

	// Wait until any ongoing room creation process is complete to avoid race conditions.
	if errWait := waitUntilRoomCreationCompletes(ctx, m.rs, roomID, m.logger); errWait != nil {
		log.WithError(errWait).Error("Cannot end room as it's locked")
		return false, fmt.Sprintf("Failed to end room: %s", errWait.Error())
	}
	log.Info("Proceeding to end room")

	// Fetch the live room state from the NATS key-value store first.
	info, err := m.natsService.GetRoomInfo(roomID)
	if err != nil {
		log.WithError(err).Warn("NATS GetRoomInfo failed during EndRoom. Falling back to DB check.")
	}

	if info == nil {
		// If NATS fails or room not in NATS, check the database.
		roomDbInfo, dbErr := m.ds.GetRoomInfoByRoomId(roomID, 1) // Using 1 for active
		if dbErr != nil {
			return false, dbErr.Error()
		}
		if roomDbInfo == nil || roomDbInfo.ID == 0 {
			return false, "room not found in DB or not active"
		}
		if roomDbInfo.IsRunning == 1 {
			log.Warn("Room active in DB but not in NATS during EndRoom. Marking as ended and cleaning up.")
			go m.OnAfterRoomEnded(roomDbInfo.ID, roomDbInfo.RoomId, roomDbInfo.Sid, "", "") // Metadata might be empty
		}
		return true, "room ended (NATS info was missing, cleanup initiated)"
	}

	// Temporarily cache the live room data in Redis.
	m.rs.HoldTemporaryRoomData(info)

	// Broadcast a 'SESSION_ENDED' event to all clients in the room.
	if err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_SESSION_ENDED, roomID, "notifications.room-disconnected-room-ended", nil); err != nil {
		log.WithError(err).Error("error sending session ended notification message")
	}

	// Trigger the main asynchronous cleanup process.
	go m.OnAfterRoomEnded(info.DbTableId, info.RoomId, info.RoomSid, info.Metadata, info.Status)
	return true, "success"
}

func (m *RoomModel) OnAfterRoomEnded(dbTableId uint64, roomID, roomSID, metadata, roomStatus string) {
	log := m.logger.WithFields(logrus.Fields{
		"room_id":     roomID,
		"room_sid":    roomSID,
		"room_status": roomStatus,
		"operation":   "OnAfterRoomEnded",
	})
	log.Info("Starting room cleanup")

	if roomStatus != natsservice.RoomStatusEnded {
		// update status immediately to prevent user to join
		if err := m.natsService.UpdateRoomStatus(roomID, natsservice.RoomStatusTriggeredEnd); err != nil {
			log.WithError(err).Error("error updating room status")
		}
	}

	// Acquire a distributed lock to prevent race conditions with room creation.
	cleanupLockTTL := config.WaitBeforeTriggerOnAfterRoomEnded + (time.Second * 10)
	lockAcquired, lockVal, errLock := m.rs.LockRoomCreation(m.ctx, roomID, cleanupLockTTL)

	if errLock != nil {
		log.WithError(errLock).Error("redis error acquiring room creation. Cleanup might be incomplete.")
		return // Can't proceed without a clear lock status.
	} else if !lockAcquired {
		log.Warn("could not acquire room creation lock. Cleanup might be incomplete.")
		return // Another process is likely handling this room.
	}
	log.WithField("lockVal", lockVal).Info("room creation lock acquired")

	// Defer the lock release to ensure it's always unlocked, even if a panic occurs.
	defer func() {
		unlockCtx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
		defer cancel()
		if err := m.rs.UnlockRoomCreation(unlockCtx, roomID, lockVal); err != nil {
			log.WithField("lockVal", lockVal).WithError(err).Error("Error releasing cleanup lock")
		} else {
			log.WithField("lockVal", lockVal).Info("room creation lock released")
		}
	}()

	// Wait for all users to disconnect before proceeding.
	m.waitForAllUsersToDisconnect(roomID)

	// send session_ended webhook before ending room in livekit
	m.sendSessionEndedWebhook(roomID, roomSID)

	if roomStatus != natsservice.RoomStatusEnded {
		err := m.natsService.UpdateRoomStatus(roomID, natsservice.RoomStatusEnded)
		if err != nil {
			log.WithError(err).Error("error updating room status")
		}
		// ensure the session is terminated in LiveKit
		_, err = m.lk.EndRoom(roomID)
		if err != nil {
			log.WithError(err).Error("error ending room in livekit")
		}
	}

	// Mark the room as not running in the database.
	_, err := m.ds.UpdateRoomStatus(&dbmodels.RoomInfo{RoomId: roomID, IsRunning: 0})
	if err != nil {
		log.WithError(err).Error("DB error updating status")
	}

	// Send a stop signal to any active recorders for this room.
	if err = m.recordingModel.DispatchRecorderTask(&plugnmeet.RecordingReq{Task: plugnmeet.RecordingTasks_STOP, Sid: roomSID, RoomId: roomID}); err != nil {
		log.WithError(err).Error("Error sending stop to recorder")
	}

	// If not configured to keep files, delete all uploaded files for this session.
	if !m.app.UploadFileSettings.KeepForever {
		if err = m.fileModel.DeleteRoomUploadedDir(roomSID); err != nil {
			log.WithError(err).Error("Error deleting uploads")
		}
	}

	// Remove the room from the duration checker if it was being monitored.
	if err = m.DeleteRoomWithDuration(roomID); err != nil {
		log.WithError(err).Error("Error deleting room duration")
	}

	// Clean up any associated Etherpad (shared notepad) pads.
	_ = m.etherpadModel.CleanAfterRoomEnd(roomID, metadata)

	// Clean up any polls created during the session.
	if err = m.pollModel.CleanUpPolls(roomID); err != nil {
		log.WithError(err).Error("Error cleaning polls")
	}

	// Perform post-end tasks for breakout rooms, if any.
	if err = m.breakoutModel.PostTaskAfterRoomEndWebhook(m.ctx, roomID, metadata); err != nil {
		log.WithError(err).Error("Error in breakout room post-end task")
	}

	// End all the agent tasks for this room.
	m.insightsModel.OnAfterRoomEnded(dbTableId, roomID, roomSID)

	// clean any SIP DispatchRule
	m.lk.DeleteSIPDispatchRule(roomID, log)

	// NOTE: ==> THIS WILL BE THE LAST <==
	// Perform the final NATS cleanup, deleting room-specific streams, and KV stores.
	m.natsService.OnAfterSessionEndCleanup(roomID)

	log.Info("Room has been cleaned properly")

	// Schedule the analytics export to run after a delay.
	// This is done asynchronously to allow the current cleanup lock to be released.
	time.AfterFunc(config.WaitBeforeAnalyticsStartProcessing, func() {
		// PrepareToExportAnalytics has it's own room creation locking logic
		m.analyticsModel.PrepareToExportAnalytics(roomID, roomSID, metadata)
	})
}

// waitForAllUsersToDisconnect waits for all users in a room to disconnect.
// It checks periodically and times out after a configured duration.
func (m *RoomModel) waitForAllUsersToDisconnect(roomID string) {
	log := m.logger.WithField("room_id", roomID)
	totalWait := config.WaitBeforeTriggerOnAfterRoomEnded
	interval := 1 * time.Second // Check every second

	timeout := time.After(totalWait)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	log.Infof("Waiting up to %s for all users to disconnect.", totalWait)

	for {
		select {
		case <-timeout:
			log.Warn("Timed out waiting for all users to disconnect. Proceeding with cleanup.")
			return
		case <-ticker.C:
			onlineUsers, err := m.natsService.GetOnlineUsersId(roomID)
			if err != nil {
				log.WithError(err).Warn("Failed to get online user list while waiting for disconnect. Proceeding with cleanup.")
				return // Exit if we can't check users
			}

			if onlineUsers == nil || len(onlineUsers) == 0 {
				log.Info("All users have disconnected. Proceeding with cleanup.")
				return // All users are gone, exit loop
			}
			log.Infof("Waiting for %d user(s) to disconnect...", len(onlineUsers))
		}
	}
}

// sendSessionEndedWebhook to send webhook
func (m *RoomModel) sendSessionEndedWebhook(roomId, roomSid string) {
	if m.webhookNotifier != nil {
		e := "session_ended"
		msg := &plugnmeet.CommonNotifyEvent{
			Event: &e,
			Room: &plugnmeet.NotifyEventRoom{
				RoomId: &roomId,
				Sid:    &roomSid,
			},
		}

		if err := m.webhookNotifier.SendWebhookEvent(msg); err != nil {
			m.logger.WithError(err).Errorln("error sending session ended webhook")
		}
	}
}
