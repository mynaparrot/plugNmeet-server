package models

import (
	"context"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/sirupsen/logrus"
)

type onAfterRoomEndedParams struct {
	dbTableId  uint64
	roomId     string
	roomSid    string
	metadata   string
	roomStatus string
	createdAt  uint64
	started    time.Time
	lockVal    string
}

// EndRoom will mark a room as ended and trigger all the post-end processes.
func (m *RoomModel) EndRoom(ctx context.Context, r *plugnmeet.RoomEndReq) (bool, string, plugnmeet.StatusCode) {
	roomID := r.GetRoomId()
	log := m.logger.WithFields(logrus.Fields{
		"room_id": roomID,
		"method":  "EndRoom",
	})
	started := time.Now()

	// Wait until any ongoing room creation process is complete to avoid race conditions.
	if errWait := waitUntilRoomCreationCompletes(ctx, m.rs, roomID, m.logger); errWait != nil {
		log.WithError(errWait).Error("Cannot end room as it's locked during creation")
		return false, "failed to end room, it may be starting", plugnmeet.StatusCode_INTERNAL_SERVER_ERROR
	}

	// Acquire a distributed lock to prevent multiple end-room processes from running simultaneously.
	roomEndLockTTL := config.WaitBeforeTriggerOnAfterRoomEnded + (time.Second * 10)
	lockAcquired, lockVal, errLock := m.rs.LockRoomCreation(m.ctx, roomID, roomEndLockTTL)

	if errLock != nil {
		log.WithError(errLock).Error("Redis error acquiring room-end lock")
		return false, "failed to end room", plugnmeet.StatusCode_INTERNAL_SERVER_ERROR
	}
	if !lockAcquired {
		log.Warn("Could not acquire room-end lock. Another end-room process is likely already running.")
		return true, "success", plugnmeet.StatusCode_SUCCESS
	}

	log.WithField("lockVal", lockVal).Info("Room-end lock acquired")

	// Fetch the live room state from the NATS key-value store first.
	info, err := m.natsService.GetRoomInfo(roomID)
	if err != nil {
		log.WithError(err).Warn("NATS GetRoomInfo failed during EndRoom. Falling back to DB check.")
	}

	if info == nil {
		// If NATS fails or room not in NATS, check the database.
		roomDbInfo, dbErr := m.ds.GetRoomInfoByRoomId(roomID, 1) // Using 1 for active
		if dbErr != nil {
			// an error occurred, we must release the lock.
			_ = m.rs.UnlockRoomCreation(ctx, roomID, lockVal)
			return false, "failed to end room", plugnmeet.StatusCode_INTERNAL_SERVER_ERROR
		}
		if roomDbInfo == nil || roomDbInfo.ID == 0 {
			_ = m.rs.UnlockRoomCreation(ctx, roomID, lockVal)
			return false, "room not found or not active", plugnmeet.StatusCode_ROOM_NOT_FOUND
		}
		if roomDbInfo.IsRunning == 1 {
			log.Warn("Room active in DB but not in NATS during EndRoom. Marking as ended and cleaning up.")
			go m.onAfterRoomEnded(&onAfterRoomEndedParams{
				dbTableId: roomDbInfo.ID,
				roomId:    roomDbInfo.RoomId,
				roomSid:   roomDbInfo.Sid,
				createdAt: uint64(roomDbInfo.CreationTime),
				started:   started,
				lockVal:   lockVal,
			})
		} else {
			// The room was found in DB but is not active, so no action is needed. Release the lock.
			log.Warn("Room found in DB but already marked as not running. Releasing lock.")
			_ = m.rs.UnlockRoomCreation(ctx, roomID, lockVal)
		}
		return true, "success", plugnmeet.StatusCode_SUCCESS
	}

	// Temporarily cache the live room data in Redis.
	// This serves as a fallback in case the 'room_finished' webhook from LiveKit is delayed.
	m.rs.HoldTemporaryRoomData(info)

	// Broadcast a 'SESSION_ENDED' event to all clients in the room.
	if err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_SESSION_ENDED, roomID, "notifications.room-disconnected-room-ended", nil); err != nil {
		log.WithError(err).Error("Error sending session ended notification message")
	}

	// Trigger the main asynchronous room-end process.
	go m.onAfterRoomEnded(&onAfterRoomEndedParams{
		dbTableId:  info.DbTableId,
		roomId:     info.RoomId,
		roomSid:    info.RoomSid,
		metadata:   info.Metadata,
		roomStatus: info.Status,
		createdAt:  info.CreatedAt,
		started:    started,
		lockVal:    lockVal,
	})
	return true, "success", plugnmeet.StatusCode_SUCCESS
}

// onAfterRoomEnded performs all the necessary tasks after a room has ended.
// This includes updating statuses, stopping recorders, cleaning up data, and triggering webhooks.
func (m *RoomModel) onAfterRoomEnded(p *onAfterRoomEndedParams) {
	log := m.logger.WithFields(logrus.Fields{
		"room_id":     p.roomId,
		"room_sid":    p.roomSid,
		"room_status": p.roomStatus,
		"method":      "onAfterRoomEnded",
	})
	log.Info("Starting room cleanup process")

	// Defer the lock release to ensure it's always unlocked, even if a panic occurs.
	defer func() {
		unlockCtx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
		defer cancel()
		if err := m.rs.UnlockRoomCreation(unlockCtx, p.roomId, p.lockVal); err != nil {
			log.WithField("lockVal", p.lockVal).WithError(err).Error("Error releasing room-end lock")
		} else {
			log.WithField("lockVal", p.lockVal).Infof("Room-end lock released after %s", time.Since(p.started))
		}
	}()

	if p.roomStatus != natsservice.RoomStatusEnded {
		// update status immediately to prevent user to join
		if err := m.natsService.UpdateRoomStatus(p.roomId, natsservice.RoomStatusTriggeredEnd); err != nil {
			log.WithError(err).Error("error updating room status")
		}
	}

	// Wait for all users to disconnect before proceeding.
	m.waitForAllUsersToDisconnect(p.roomId)

	// send session_ended webhook before ending room in livekit
	m.sendSessionEndedWebhook(p.roomId, p.roomSid, p.metadata, p.createdAt)

	if p.roomStatus != natsservice.RoomStatusEnded {
		if err := m.natsService.UpdateRoomStatus(p.roomId, natsservice.RoomStatusEnded); err != nil {
			log.WithError(err).Error("Error updating room status")
		}
		// ensure the session is terminated in LiveKit
		if _, err := m.lk.EndRoom(p.roomId); err != nil {
			log.WithError(err).Error("Error ending room in livekit")
		}
	}

	// Mark the room as not running in the database.
	if _, err := m.ds.UpdateRoomStatus(&dbmodels.RoomInfo{RoomId: p.roomId, IsRunning: 0}); err != nil {
		log.WithError(err).Error("DB error updating status")
	}

	// Send a stop signal to any active recorders for this room.
	_ = m.recordingModel.DispatchRecorderTask(&plugnmeet.RecordingReq{
		Task:        plugnmeet.RecordingTasks_STOP,
		Sid:         p.roomSid,
		RoomId:      p.roomId,
		RoomTableId: int64(p.dbTableId),
	})

	// If not configured to keep files, delete all uploaded files for this session.
	if !m.app.UploadFileSettings.KeepForever {
		if err := m.fileModel.DeleteRoomUploadedDir(p.roomSid); err != nil {
			log.WithError(err).Error("Error deleting uploads")
		}
	}

	// Remove the room from the duration checker if it was being monitored.
	if err := m.DeleteRoomWithDuration(p.roomId); err != nil {
		log.WithError(err).Error("Error deleting room duration")
	}

	// Clean up any associated Etherpad (shared notepad) pads.
	_ = m.etherpadModel.CleanAfterRoomEnd(p.roomId, p.metadata)

	// Clean up any polls created during the session.
	if err := m.pollModel.CleanUpPolls(p.roomId); err != nil {
		log.WithError(err).Error("Error cleaning polls")
	}

	// Perform post-end tasks for breakout rooms, if any.
	if err := m.breakoutModel.PostTaskAfterRoomEndWebhook(m.ctx, p.roomId, p.metadata); err != nil {
		log.WithError(err).Error("Error in breakout room post-end task")
	}

	// End all the agent tasks for this room.
	m.insightsModel.OnAfterRoomEnded(p.dbTableId, p.roomId, p.roomSid)

	// clean any SIP DispatchRule
	m.lk.DeleteSIPDispatchRule(p.roomId, log)

	// CRITICAL: ==> THIS WILL BE THE LAST <==
	// Final NATS cleanup: deletes all consumers, messages, and the KV store for this room.
	m.natsService.OnAfterSessionEndCleanup(p.roomId)

	log.Infof("Room has been ended properly after %s", time.Since(p.started))

	// Schedule the analytics export to run after a delay.
	// This is done asynchronously to allow the current room-end lock to be released.
	time.AfterFunc(config.WaitBeforeAnalyticsStartProcessing, func() {
		// PrepareToExportAnalytics has it's own room creation locking logic
		m.analyticsModel.PrepareToExportAnalytics(p.roomId, p.roomSid, p.metadata)
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
			log.Warn("Timed out waiting for all users to disconnect. Proceeding with room-end process.")
			return
		case <-ticker.C:
			onlineUsers, err := m.natsService.GetOnlineUsersId(roomID)
			if err != nil {
				log.WithError(err).Warn("Failed to get online user list while waiting for disconnect. Proceeding with room-end process.")
				return // Exit if we can't check users
			}

			if onlineUsers == nil || len(onlineUsers) == 0 {
				log.Info("All users have disconnected. Proceeding with room-end process.")
				return // All users are gone, exit loop
			}
			log.Infof("Waiting for %d user(s) to disconnect...", len(onlineUsers))
		}
	}
}

// sendSessionEndedWebhook to send webhook
func (m *RoomModel) sendSessionEndedWebhook(roomId, roomSid, metadata string, createdAt uint64) {
	if m.webhookNotifier != nil {
		msg := &plugnmeet.CommonNotifyEvent{
			Event: new("session_ended"),
			Room: &plugnmeet.NotifyEventRoom{
				RoomId:       &roomId,
				Sid:          &roomSid,
				Metadata:     &metadata,
				CreationTime: &createdAt,
			},
		}

		if err := m.webhookNotifier.SendWebhookEvent(msg); err != nil {
			m.logger.WithError(err).Errorln("error sending session ended webhook")
		}
	}
}
