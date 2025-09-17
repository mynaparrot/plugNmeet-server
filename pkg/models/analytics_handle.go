package models

import (
	"fmt"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

const (
	analyticsRoomKey = "pnm:analytics:%s"
	analyticsUserKey = analyticsRoomKey + ":user:%s"
)

func (m *AnalyticsModel) HandleEvent(d *plugnmeet.AnalyticsDataMsg) {
	if config.GetConfig().AnalyticsSettings == nil ||
		!config.GetConfig().AnalyticsSettings.Enabled {
		return
	}
	m.Lock()
	defer m.Unlock()
	// we'll use unix milliseconds to make sure fields are unique
	d.Time = time.Now().UnixMilli()
	m.data = d

	switch d.EventType {
	case plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM:
		m.handleRoomTypeEvents()
	case plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_USER:
		m.handleUserTypeEvents()
	}
}

func (m *AnalyticsModel) handleRoomTypeEvents() {
	if m.data.EventName == plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_UNKNOWN {
		return
	}
	key := fmt.Sprintf(analyticsRoomKey+":room", m.data.RoomId)

	switch m.data.GetEventName() {
	case plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_JOINED:
		m.handleFirstTimeUserJoined(key)
		// we still need to run as the user type too
		m.handleUserTypeEvents()
	default:
		m.insertEventData(key)
	}
}

func (m *AnalyticsModel) handleUserTypeEvents() {
	if m.data.EventName == plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_UNKNOWN {
		return
	}
	key := fmt.Sprintf(analyticsUserKey, m.data.RoomId, m.data.GetUserId())
	m.insertEventData(key)
}
