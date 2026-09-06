package models

import (
	"errors"
	"strconv"
	"strings"
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
		// poll gone (e.g. room ended): purge any leaked duration-index hint;
		// a missing poll can never be reopened
		if err := m.rs.RemovePollWithDuration(roomId, pollId); err != nil {
			log.WithError(err).Errorln("failed to purge duration index entry for missing poll")
		}
		return nil
	}
	info := new(plugnmeet.PollInfo)
	if err = protojson.Unmarshal([]byte(pi), info); err != nil {
		log.WithError(err).Errorln("failed to unmarshal stored poll info")
		return err
	}
	if !info.IsRunning {
		return nil // already closed
	}
	// the janitor's index is only a hint of what to check; re-validate against
	// the poll's current expiry so a just-reopened poll (which restarts its
	// expires_at) isn't closed early. ExpiresAt == 0 means no limit.
	if info.ExpiresAt > 0 && time.Now().Unix() < info.ExpiresAt {
		return nil // not due yet
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

// CloseExpiredPolls sweeps the duration index and auto-closes polls past
// their expires_at. Called by the janitor on every tick. The
// pnm:pollsWithDuration hash is only a HINT of what to check; AutoClosePoll
// re-validates against the poll's current ExpiresAt, which is the truth.
func (m *PollModel) CloseExpiredPolls() {
	log := m.logger.WithField("task", "close-expired-polls")

	entries, err := m.rs.GetPollsWithDuration()
	if err != nil {
		log.WithError(err).Errorln("failed to fetch polls with duration from redis")
		return
	}

	now := time.Now().Unix()
	for field, val := range entries {
		// pollIds are UUIDs and never contain ":"; roomIds may, so split on
		// the last colon only
		idx := strings.LastIndex(field, ":")
		if idx <= 0 {
			log.WithField("field", field).Warn("malformed duration index field; skipping")
			continue
		}
		roomId, pollId := field[:idx], field[idx+1:]

		// value is the poll's expires_at as a unix-seconds decimal string
		expiresAt, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			// the field parsed fine, so the entry itself is garbage: self-heal it
			log.WithField("field", field).WithField("value", val).WithError(err).Warn("malformed duration index value; removing entry")
			if rmErr := m.rs.RemovePollWithDuration(roomId, pollId); rmErr != nil {
				log.WithError(rmErr).Errorln("failed to remove malformed duration index entry")
			}
			continue
		}

		if now < expiresAt {
			continue // not due yet
		}

		if err := m.AutoClosePoll(roomId, pollId); err != nil {
			log.WithFields(logrus.Fields{
				"roomId": roomId,
				"pollId": pollId,
			}).WithError(err).Errorln("failed to auto close expired poll")
		}
	}
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
