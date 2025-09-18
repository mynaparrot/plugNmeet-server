package models

import (
	"fmt"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

func (m *AnalyticsModel) HandleEvent(d *plugnmeet.AnalyticsDataMsg) {
	if m.app.AnalyticsSettings == nil ||
		!m.app.AnalyticsSettings.Enabled {
		return
	}
	// we'll use unix milliseconds to make sure fields are unique
	d.Time = time.Now().UnixMilli()

	switch d.EventType {
	case plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM:
		m.handleRoomTypeEvents(d)
	case plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_USER:
		m.handleUserTypeEvents(d)
	}
}

func (m *AnalyticsModel) handleRoomTypeEvents(d *plugnmeet.AnalyticsDataMsg) {
	if d.EventName == plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_UNKNOWN {
		return
	}
	key := fmt.Sprintf(analyticsRoomKey+":room", d.RoomId)

	switch d.GetEventName() {
	case plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_JOINED:
		m.handleFirstTimeUserJoined(d, key)
		// The user-specific event insertion is now handled inside handleFirstTimeUserJoined
	default:
		m.insertEventData(d, key)
	}
}

func (m *AnalyticsModel) handleUserTypeEvents(d *plugnmeet.AnalyticsDataMsg) {
	if d.EventName == plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_UNKNOWN {
		return
	}
	key := fmt.Sprintf(analyticsUserKey, d.RoomId, d.GetUserId())
	m.insertEventData(d, key)
}
