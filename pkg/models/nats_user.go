package models

import (
	"fmt"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/sirupsen/logrus"
)

func (m *NatsModel) OnAfterUserJoined(roomId, userId string) {
	status, err := m.natsService.GetRoomUserStatus(roomId, userId)
	// If there's an error or the user is already online, we don't need to proceed.
	if err != nil || status == natsservice.UserStatusOnline {
		if err != nil {
			m.logger.WithFields(logrus.Fields{
				"room_id": roomId,
				"user_id": userId,
			}).Errorf("failed to get room user status: %v", err)
		}
		return
	}

	if err = m.natsService.UpdateUserStatus(roomId, userId, natsservice.UserStatusOnline); err != nil {
		m.logger.WithFields(logrus.Fields{
			"room_id": roomId,
			"user_id": userId,
			"status":  natsservice.UserStatusOnline,
		}).Warnf("failed to update user status: %v", err)
	}

	userInfo, err := m.natsService.GetUserInfo(roomId, userId)
	if err == nil && userInfo != nil {
		// broadcast this user to everyone
		_ = m.natsService.BroadcastSystemEventToEveryoneExceptUserId(plugnmeet.NatsMsgServerToClientEvents_USER_JOINED, roomId, userInfo, userId)

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
	} else if err != nil {
		m.logger.WithFields(logrus.Fields{
			"room_id": roomId,
			"user_id": userId,
		}).Warnf("could not get user info after join: %v", err)
	}
}

// OnAfterUserDisconnected should be run in separate goroutine
// we'll wait for 5 seconds before declare user as offline
// but will broadcast as disconnected
func (m *NatsModel) OnAfterUserDisconnected(roomId, userId string) {
	// Immediately set status to disconnected and notify clients.
	_ = m.natsService.UpdateUserStatus(roomId, userId, natsservice.UserStatusDisconnected)

	// Try to get user info for a richer disconnect message.
	userInfo, err := m.natsService.GetUserInfo(roomId, userId)
	if err != nil || userInfo == nil {
		// If we can't get user info, send a basic event and update analytics.
		_ = m.natsService.BroadcastSystemEventToEveryoneExceptUserId(plugnmeet.NatsMsgServerToClientEvents_USER_DISCONNECTED, roomId, &plugnmeet.NatsKvUserInfo{UserId: userId}, userId)
	} else {
		_ = m.natsService.BroadcastSystemEventToEveryoneExceptUserId(plugnmeet.NatsMsgServerToClientEvents_USER_DISCONNECTED, roomId, userInfo, userId)
	}

	// Start a non-blocking background task to handle the full offline/cleanup lifecycle.
	go m.handleDelayedOfflineTasks(roomId, userId, userInfo)
}

// handleDelayedOfflineTasks manages the grace period for user reconnection and subsequent cleanup.
func (m *NatsModel) handleDelayedOfflineTasks(roomId, userId string, userInfo *plugnmeet.NatsKvUserInfo) {
	// Stage 1: Wait for the reconnection grace period.
	time.Sleep(5 * time.Second)

	status, err := m.natsService.GetRoomUserStatus(roomId, userId)
	if err == nil && status == natsservice.UserStatusOnline {
		// User reconnected, do nothing.
		return
	}

	// User is still disconnected, so mark as offline.
	if err = m.natsService.UpdateUserStatus(roomId, userId, natsservice.UserStatusOffline); err != nil {
		m.logger.WithFields(logrus.Fields{
			"room_id": roomId,
			"user_id": userId,
			"status":  natsservice.UserStatusOffline,
		}).Warnf("failed to update user status: %v", err)
	}

	// Send analytics for the user leaving.
	m.updateUserLeftAnalytics(roomId, userId)

	// Broadcast the final offline status.
	if userInfo != nil {
		_ = m.natsService.BroadcastSystemEventToEveryoneExceptUserId(plugnmeet.NatsMsgServerToClientEvents_USER_OFFLINE, roomId, userInfo, userId)
	} else {
		// Fallback if userInfo was not available initially.
		_ = m.natsService.BroadcastSystemEventToEveryoneExceptUserId(plugnmeet.NatsMsgServerToClientEvents_USER_OFFLINE, roomId, &plugnmeet.NatsKvUserInfo{UserId: userId}, userId)
	}

	// Stage 2: Wait a bit longer before cleaning up resources.
	time.Sleep(30 * time.Second)

	status, err = m.natsService.GetRoomUserStatus(roomId, userId)
	if err == nil && status == natsservice.UserStatusOnline {
		// User reconnected, do not delete consumer.
		return
	}

	// Final cleanup: Delete the user's NATS consumer.
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
