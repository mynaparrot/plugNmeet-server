package models

import (
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
)

const (
	countdownTimerMaxDurationSeconds = 24 * 60 // 1 hour

	countdownTimerStatusIdle    = "idle"
	countdownTimerStatusRunning = "running"
	countdownTimerStatusPaused  = "paused"
)

// HandleCountdownTimerTask updates the room's shared countdown timer state.
// Validation failures are returned as i18n keys the client resolves via t().
func (m *RoomModel) HandleCountdownTimerTask(req *plugnmeet.CountdownTimerReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": req.RoomId,
		"userId": req.UserId,
		"task":   req.Task.String(),
		"method": "HandleCountdownTimerTask",
	})

	roomMeta, err := m.natsService.GetRoomMetadataStruct(req.RoomId)
	if err != nil {
		log.WithError(err).Errorln("failed to get room metadata")
		return config.ErrCountdownTimerGeneric
	}
	if roomMeta == nil {
		log.Errorln("invalid nil room metadata information")
		return config.ErrCountdownTimerGeneric
	}

	cf := roomMeta.GetRoomFeatures().GetCountdownTimerFeatures()
	if cf == nil || !cf.IsAllow {
		log.Warnln("countdown timer feature is not allowed for this room")
		return config.ErrCountdownTimerNotAllowed
	}

	if err = applyCountdownTimerTask(cf, req, time.Now().UnixMilli()); err != nil {
		return err
	}

	if err = m.natsService.UpdateAndBroadcastRoomMetadata(req.RoomId, roomMeta); err != nil {
		log.WithError(err).Errorln("failed to update and broadcast room metadata")
		return config.ErrCountdownTimerGeneric
	}

	if req.Task == plugnmeet.CountdownTimerTask_CHANGE_STATUS {
		m.sendCountdownTimerAnalytics(req.RoomId, cf.IsActive)
	}

	return nil
}

// applyCountdownTimerTask applies a timer task to the feature state.
// nowMs is the current unix time in milliseconds.
func applyCountdownTimerTask(cf *plugnmeet.CountdownTimerFeatures, req *plugnmeet.CountdownTimerReq, nowMs int64) error {
	switch req.Task {
	case plugnmeet.CountdownTimerTask_CHANGE_STATUS:
		if req.IsActive == nil {
			return config.ErrCountdownTimerGeneric
		}
		cf.IsActive = *req.IsActive
		cf.Status = ""
		if cf.IsActive {
			cf.Status = countdownTimerStatusIdle
		}
		resetCountdownTimerRuntime(cf)

	case plugnmeet.CountdownTimerTask_START_TIMER:
		if !cf.IsActive {
			return config.ErrCountdownTimerNotActive
		}

		duration := req.GetDurationSeconds()
		if duration == 0 || duration > countdownTimerMaxDurationSeconds {
			return config.ErrCountdownTimerInvalidDuration
		}
		cf.Status = countdownTimerStatusRunning
		cf.DurationSeconds = duration
		cf.EndTime = nowMs + int64(duration)*1000
		cf.RemainingSeconds = 0
		cf.StartedBy = req.UserId

	case plugnmeet.CountdownTimerTask_PAUSE_TIMER:
		if !cf.IsActive || cf.Status != countdownTimerStatusRunning {
			return config.ErrCountdownTimerNotRunning
		}
		remMs := cf.EndTime - nowMs
		if remMs < 0 {
			remMs = 0
		}
		remSec := uint32((remMs + 999) / 1000)
		if remSec > cf.DurationSeconds {
			remSec = cf.DurationSeconds
		}
		cf.Status = countdownTimerStatusPaused
		cf.RemainingSeconds = remSec
		cf.EndTime = 0

	case plugnmeet.CountdownTimerTask_RESUME_TIMER:
		if !cf.IsActive || cf.Status != countdownTimerStatusPaused || cf.RemainingSeconds == 0 {
			return config.ErrCountdownTimerNotPaused
		}
		cf.Status = countdownTimerStatusRunning
		cf.EndTime = nowMs + int64(cf.RemainingSeconds)*1000
		cf.RemainingSeconds = 0

	case plugnmeet.CountdownTimerTask_EXTEND_TIMER:
		if !cf.IsActive || (cf.Status != countdownTimerStatusRunning && cf.Status != countdownTimerStatusPaused) {
			return config.ErrCountdownTimerNotRunning
		}
		extendSec := req.GetDurationSeconds()
		if extendSec == 0 {
			extendSec = 60
		}
		cf.DurationSeconds += extendSec
		if cf.Status == countdownTimerStatusRunning {
			base := cf.EndTime
			if base < nowMs {
				base = nowMs
			}
			cf.EndTime = base + int64(extendSec)*1000
		} else {
			cf.RemainingSeconds += extendSec
		}

	case plugnmeet.CountdownTimerTask_STOP_TIMER:
		if !cf.IsActive {
			return config.ErrCountdownTimerNotActive
		}
		cf.Status = countdownTimerStatusIdle
		resetCountdownTimerRuntime(cf)

	default:
		return config.ErrCountdownTimerGeneric
	}

	return nil
}

func resetCountdownTimerRuntime(cf *plugnmeet.CountdownTimerFeatures) {
	cf.DurationSeconds = 0
	cf.EndTime = 0
	cf.RemainingSeconds = 0
	cf.StartedBy = ""
}

func (m *RoomModel) sendCountdownTimerAnalytics(roomId string, isActive bool) {
	val := plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_STARTED.String()
	if !isActive {
		val = plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_ENDED.String()
	}

	m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_COUNTDOWN_TIMER_STATUS,
		RoomId:    roomId,
		HsetValue: &val,
	})
}
