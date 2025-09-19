package models

import (
	"fmt"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
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

	// Try to get user info for a richer disconnect message.
	userInfo, err := m.natsService.GetUserInfo(roomId, userId)
	if err != nil || userInfo == nil {
		// If we can't get user info, send a basic event and update analytics.
		if err = m.natsService.BroadcastSystemEventToEveryoneExceptUserId(plugnmeet.NatsMsgServerToClientEvents_USER_DISCONNECTED, roomId, &plugnmeet.NatsKvUserInfo{UserId: userId}, userId); err != nil {
			log.WithError(err).Error("failed to broadcast basic USER_DISCONNECTED event")
		}
	} else {
		if err = m.natsService.BroadcastSystemEventToEveryoneExceptUserId(plugnmeet.NatsMsgServerToClientEvents_USER_DISCONNECTED, roomId, userInfo, userId); err != nil {
			log.WithError(err).Warn("failed to broadcast USER_DISCONNECTED event")
		}
	}

	// Start a non-blocking background task to handle the full offline/cleanup lifecycle.
	go m.handleDelayedOfflineTasks(roomId, userId, userInfo, log)
}

// handleDelayedOfflineTasks manages the grace period for user reconnection and subsequent cleanup.
func (m *NatsModel) handleDelayedOfflineTasks(roomId, userId string, userInfo *plugnmeet.NatsKvUserInfo, log *logrus.Entry) {
	log = log.WithField("subMethod", "handleDelayedOfflineTasks")
	log.Info("starting delayed offline tasks")

	// Stage 1: Wait for the reconnection grace period.
	time.Sleep(5 * time.Second)

	status, err := m.natsService.GetRoomUserStatus(roomId, userId)
	if err == nil && status == natsservice.UserStatusOnline {
		// User reconnected, do nothing.
		log.Info("user reconnected within grace period, aborting offline tasks")
		return
	}

	// User is still disconnected, so mark as offline.
	if err = m.natsService.UpdateUserStatus(roomId, userId, natsservice.UserStatusOffline); err != nil {
		log.WithError(err).WithField("status", natsservice.UserStatusOffline).Warn("failed to update user status")
	}

	// Send analytics for the user leaving.
	m.updateUserLeftAnalytics(roomId, userId)

	// Broadcast the final offline status.
	if userInfo != nil {
		if err = m.natsService.BroadcastSystemEventToEveryoneExceptUserId(plugnmeet.NatsMsgServerToClientEvents_USER_OFFLINE, roomId, userInfo, userId); err != nil {
			log.WithError(err).Warn("failed to broadcast USER_OFFLINE event")
		}
	} else {
		// Fallback if userInfo was not available initially.
		if err = m.natsService.BroadcastSystemEventToEveryoneExceptUserId(plugnmeet.NatsMsgServerToClientEvents_USER_OFFLINE, roomId, &plugnmeet.NatsKvUserInfo{UserId: userId}, userId); err != nil {
			log.WithError(err).Error("failed to broadcast basic USER_OFFLINE event")
		}
	}

	// Stage 2: Wait a bit longer before cleaning up resources.
	time.Sleep(30 * time.Second)

	status, err = m.natsService.GetRoomUserStatus(roomId, userId)
	if err == nil && status == natsservice.UserStatusOnline {
		// User reconnected, do not delete consumer.
		log.Info("user reconnected before final cleanup, consumer will not be deleted")
		return
	}

	// Final cleanup: Delete the user's NATS consumer.
	log.Info("deleting user's NATS consumer")
	m.natsService.DeleteConsumer(roomId, userId)
}

func (m *NatsModel) updateUserLeftAnalytics(roomId, userId string) {
	now := fmt.Sprintf("%d", time.Now().UnixMilli())
	m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_USER,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_LEFT,
		RoomId:    roomId,
		UserId:    &userId,
		HsetValue: &now,
	})
}
