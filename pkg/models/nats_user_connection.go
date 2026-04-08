package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/turn"
	"github.com/sirupsen/logrus"
)

func (m *NatsModel) OnAfterUserJoined(roomId, userId string) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": roomId,
		"userId": userId,
		"method": "OnAfterUserJoined",
	})

	status, err := m.natsService.GetRoomUserStatus(roomId, userId)
	// If there's an error or the user is already online, we don't need to proceed.
	if err != nil {
		log.WithError(err).Error("failed to get room user status")
		return
	}
	if status == natsservice.UserStatusOnline {
		// This is a frequent case due to pings, so we don't log it to avoid noise.
		return
	}

	log.Info("handling user joined event")
	if err = m.natsService.UpdateUserStatus(roomId, userId, natsservice.UserStatusOnline); err != nil {
		log.WithError(err).WithField("status", natsservice.UserStatusOnline).Warn("failed to update user status")
	}

	userInfo, err := m.natsService.GetUserInfo(roomId, userId)
	if err == nil && userInfo != nil {
		// broadcast this user to everyone
		if err = m.natsService.BroadcastSystemEventToEveryoneExceptUserId(plugnmeet.NatsMsgServerToClientEvents_USER_JOINED, roomId, userInfo, userId); err != nil {
			log.WithError(err).Error("failed to broadcast USER_JOINED event")
		}

		now := fmt.Sprintf("%d", time.Now().UnixMilli())
		m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
			EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
			EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_JOINED,
			RoomId:    roomId,
			UserId:    &userId,
			UserName:  &userInfo.Name,
			ExtraData: &userInfo.Metadata,
			HsetValue: &now,
		})
		log.Info("successfully processed user joined event")

		roomInfo, err := m.natsService.GetRoomInfo(roomId)
		if err != nil {
			log.WithError(err).Error("failed to get room info")
		}
		if roomInfo != nil {
			if _, err := m.ds.IncrementOrDecrementNumParticipants(roomInfo.GetRoomSid(), "+"); err != nil {
				log.WithError(err).Error("failed to increment num participants")
			}
		}
	} else if err != nil {
		log.WithError(err).Warn("could not get user info after join")
	}
}

// OnAfterUserDisconnected should be run in separate goroutine
// we'll wait for 5 seconds before declare user as offline
// but will broadcast as disconnected
func (m *NatsModel) OnAfterUserDisconnected(roomId, userId string) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": roomId,
		"userId": userId,
		"method": "OnAfterUserDisconnected",
	})
	log.Info("handling user disconnected event")

	// Immediately set status to disconnected and notify clients.
	if err := m.natsService.UpdateUserStatus(roomId, userId, natsservice.UserStatusDisconnected); err != nil {
		log.WithError(err).WithField("status", natsservice.UserStatusDisconnected).Warn("failed to update user status")
	}

	// update analytics & db for the user leaving.
	m.updateUserLeftAnalytics(roomId, userId, log)

	// Try to get user info for a richer disconnect message.
	userInfo, err := m.natsService.GetUserInfo(roomId, userId)
	if err != nil || userInfo == nil {
		// If we can't get user info, send a basic event and update analytics.
		if err = m.natsService.BroadcastSystemEventToEveryoneExceptUserId(plugnmeet.NatsMsgServerToClientEvents_USER_DISCONNECTED, roomId, &plugnmeet.NatsKvUserInfo{UserId: userId, RoomId: roomId}, userId); err != nil {
			log.WithError(err).Error("failed to broadcast basic USER_DISCONNECTED event")
		}
	} else {
		_ = m.natsService.BroadcastSystemEventToEveryoneExceptUserId(plugnmeet.NatsMsgServerToClientEvents_USER_DISCONNECTED, roomId, userInfo, userId)
	}

	// Start a non-blocking background task to handle the full offline/cleanup lifecycle.
	go m.handleDelayedOfflineTasks(roomId, userId, userInfo, log)
}

// handleDelayedOfflineTasks manages the grace period for user reconnection and subsequent cleanup using periodic checks.
func (m *NatsModel) handleDelayedOfflineTasks(roomId, userId string, userInfo *plugnmeet.NatsKvUserInfo, log *logrus.Entry) {
	log = log.WithField("subMethod", "handleDelayedOfflineTasks")
	log.Info("starting delayed offline tasks with periodic checks")

	// get TurnCredentials to use it later otherwise it may cleaned up if room ended
	turnCreds, _ := m.natsService.GetUserTurnCredentials(roomId, userId)

	// Stage 1: Wait for the reconnection grace period (5s), checking every second.
	reconnected, roomEnded := m.waitForReconnect(roomId, userId, 5*time.Second, 1*time.Second, log)
	if reconnected {
		log.Info("user reconnected within grace period, aborting offline tasks")
		return
	}

	// User is still disconnected, so mark as offline.
	// We only log an error if the room hasn't ended, as the user's KV will be deleted soon anyway.
	if err := m.natsService.UpdateUserStatus(roomId, userId, natsservice.UserStatusOffline); err != nil && !roomEnded {
		log.WithError(err).Warn("failed to update user status to offline")
	}

	// Broadcast the final offline status.
	if userInfo != nil {
		if err := m.natsService.BroadcastSystemEventToEveryoneExceptUserId(plugnmeet.NatsMsgServerToClientEvents_USER_OFFLINE, roomId, userInfo, userId); err != nil {
			if !errors.Is(err, config.NoOnlineUserFound) {
				log.WithError(err).Warn("failed to broadcast USER_OFFLINE event")
			}
		}
	} else {
		// Fallback if userInfo was not available initially.
		_ = m.natsService.BroadcastSystemEventToEveryoneExceptUserId(plugnmeet.NatsMsgServerToClientEvents_USER_OFFLINE, roomId, &plugnmeet.NatsKvUserInfo{UserId: userId, RoomId: roomId}, userId)
	}

	// If the room ended during Stage 1, skip Stage 2 and go straight to cleanup.
	if roomEnded {
		log.Info("room ended during grace period, skipping second wait and proceeding to final cleanup")
	} else {
		// Stage 2: Wait a bit longer (30s) before cleaning up, checking for changes every 5 seconds.
		if reconnected, _ = m.waitForReconnect(roomId, userId, 30*time.Second, 5*time.Second, log); reconnected {
			log.Info("user reconnected before final cleanup, consumer will not be deleted")
			return
		}
	}

	// also try to silently remove this user from livekit as well
	_, _ = m.lk.RemoveParticipant(roomId, userId)

	// Final cleanup for a user leaving an active room.
	// Note: this is redundant if the room has ended, as OnAfterSessionEndCleanup will sweep all consumers.
	m.natsService.DeleteConsumer(roomId, userId)

	if turnCreds != nil {
		// Try to revoke TURN credentials as the very last step.
		m.revokeTurnCredentials(turnCreds, log)
	}

	log.Info("user offline tasks completed")
}

// waitForReconnect periodically checks for user reconnection or if the room has ended.
// Returns (reconnected bool, roomEnded bool).
func (m *NatsModel) waitForReconnect(roomId, userId string, totalWait, interval time.Duration, log *logrus.Entry) (reconnected bool, roomEnded bool) {
	// It checks for user reconnection or if the room has ended.
	checkStatus := func() (bool, bool) {
		// 1. Check if user reconnected (highest priority)
		status, err := m.natsService.GetRoomUserStatus(roomId, userId)
		if err == nil && status == natsservice.UserStatusOnline {
			return true, false // User reconnected.
		}

		// 2. Check if room has ended.
		roomInfo, _ := m.natsService.GetRoomInfo(roomId) // Ignore error, nil info is a valid signal.
		if roomInfo == nil {
			log.Info("room info not found, assuming it has ended.")
			return false, true // Room has ended.
		}
		if roomInfo.Status == natsservice.RoomStatusEnded || roomInfo.Status == natsservice.RoomStatusTriggeredEnd {
			log.Info("room has ended, proceeding to next cleanup step")
			return false, true // Room has ended.
		}

		return false, false // Neither condition met.
	}

	// Perform an immediate check before starting the ticker.
	if reconnected, roomEnded = checkStatus(); reconnected || roomEnded {
		return
	}

	// If not immediately resolved, start the ticker.
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	timeout := time.After(totalWait)

	for {
		select {
		case <-timeout:
			// Wait period is over.
			return false, false
		case <-ticker.C:
			if reconnected, roomEnded = checkStatus(); reconnected || roomEnded {
				return
			}
		}
	}
}

func (m *NatsModel) revokeTurnCredentials(creds *turn.Credentials, log *logrus.Entry) {
	ctx, cancel := context.WithTimeout(m.app.GetApplicationCtx(), 5*time.Second)
	defer cancel()
	if err := m.turn.RevokeCredentials(ctx, creds); err != nil {
		log.WithError(err).Warn("failed to revoke turn credentials")
	} else {
		log.Info("successfully revoked turn credentials")
	}
}

func (m *NatsModel) updateUserLeftAnalytics(roomId, userId string, log *logrus.Entry) {
	now := fmt.Sprintf("%d", time.Now().UnixMilli())
	m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_USER,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_LEFT,
		RoomId:    roomId,
		UserId:    &userId,
		HsetValue: &now,
	})

	roomInfo, err := m.natsService.GetRoomInfo(roomId)
	if err != nil {
		log.WithError(err).Error("failed to get room info")
	}
	if roomInfo != nil {
		if _, err := m.ds.IncrementOrDecrementNumParticipants(roomInfo.GetRoomSid(), "-"); err != nil {
			log.WithError(err).Error("failed to increment num participants")
		}
	}
}
