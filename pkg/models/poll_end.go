package models

import (
	"errors"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
)

func (m *PollModel) ClosePoll(r *plugnmeet.ClosePollReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": r.RoomId,
		"userId": r.UserId,
		"pollId": r.PollId,
		"method": "ClosePoll",
	})
	log.Infoln("request to close poll")

	err := m.rs.ClosePoll(r)
	if err != nil {
		// not-found is an expected user error; don't spam the log with it
		if errors.Is(err, config.ErrPollNotFound) {
			return err
		}
		log.WithError(err).Errorln("failed to close poll in redis")
		return config.ErrPollGeneric
	}

	m.afterPollClosed(r.RoomId, r.PollId)

	log.Info("successfully closed poll")
	return nil
}

// afterPollClosed emits the side effects shared by manual and auto close:
// the NATS POLL_CLOSED broadcast + the room-poll-ended analytics event.
func (m *PollModel) afterPollClosed(roomId, pollId string) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": roomId,
		"pollId": pollId,
	})

	err := m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_POLL_CLOSED, roomId, pollId, nil)
	if err != nil {
		log.WithError(err).Errorln("error sending POLL_CLOSED event")
	}

	// send analytics
	m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_POLL_ENDED,
		RoomId:    roomId,
		HsetValue: &pollId,
	})
}

// AutoClosePoll closes an expired poll with the same side effects as a manual
// close; idempotent — no-op when the poll is missing or already closed.
func (m *PollModel) AutoClosePoll(roomId, pollId string) error {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": roomId,
		"pollId": pollId,
		"method": "AutoClosePoll",
	})

	pi, err := m.rs.GetPollInfoByPollId(roomId, pollId)
	if err != nil {
		log.WithError(err).Errorln("failed to fetch poll info")
		return err
	}
	if pi == "" {
		return nil // poll gone (e.g. room ended); nothing to close
	}
	info := new(plugnmeet.PollInfo)
	if err = protojson.Unmarshal([]byte(pi), info); err != nil {
		log.WithError(err).Errorln("failed to unmarshal stored poll info")
		return err
	}
	if !info.IsRunning {
		return nil // already closed
	}

	closed, err := m.rs.ClosePollIfRunning(roomId, pollId, config.PollAutoClosedBy)
	if err != nil {
		log.WithError(err).Errorln("failed to auto close poll in redis")
		return err
	}
	if closed {
		log.Info("poll auto closed at expiry")
		m.afterPollClosed(roomId, pollId)
	}
	return nil
}

// maybeAutoCloseExpired lazily auto-closes a running poll past its expires_at;
// recovers timers lost to a server restart. Cheap: only acts when expired.
func (m *PollModel) maybeAutoCloseExpired(info *plugnmeet.PollInfo) {
	if !info.IsRunning || info.ExpiresAt <= 0 || time.Now().Unix() < info.ExpiresAt {
		return
	}
	// reuse the auto-close path; the caller treats the poll as closed after
	if err := m.AutoClosePoll(info.RoomId, info.Id); err == nil {
		info.IsRunning = false
	}
}

func (m *PollModel) CleanUpPolls(roomId string) error {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": roomId,
		"method": "CleanUpPolls",
	})
	log.Infoln("Cleaning up polls for room")

	// Directly fetch poll IDs instead of the full poll objects.
	pIds, err := m.rs.GetPollIdsByRoomId(roomId)
	if err != nil {
		log.WithError(err).Errorln("failed to get poll ids from redis")
		return err
	}

	if len(pIds) == 0 {
		log.Info("No polls to clean up")
		return nil // No polls to clean up.
	}

	err = m.rs.CleanUpPolls(roomId, pIds)
	if err != nil {
		log.WithError(err).Errorln("failed to clean up polls from redis")
		return err
	}

	log.Info("Successfully cleaned up polls")
	return nil
}
