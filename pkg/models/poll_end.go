package models

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"
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
		log.WithError(err).Errorln("failed to close poll in redis")
		return err
	}

	err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_POLL_CLOSED, r.RoomId, r.PollId, nil)
	if err != nil {
		log.WithError(err).Errorln("error sending POLL_CLOSED event")
	}

	// send analytics
	m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_POLL_ENDED,
		RoomId:    r.RoomId,
		HsetValue: &r.PollId,
	})

	log.Info("successfully closed poll")
	return nil
}

func (m *PollModel) CleanUpPolls(roomId string) error {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": roomId,
		"method": "CleanUpPolls",
	})
	log.Infoln("cleaning up polls for room")

	// Directly fetch poll IDs instead of the full poll objects.
	pIds, err := m.rs.GetPollIdsByRoomId(roomId)
	if err != nil {
		log.WithError(err).Errorln("failed to get poll ids from redis")
		return err
	}

	if len(pIds) == 0 {
		log.Info("no polls to clean up")
		return nil // No polls to clean up.
	}

	err = m.rs.CleanUpPolls(roomId, pIds)
	if err != nil {
		log.WithError(err).Errorln("failed to clean up polls from redis")
		return err
	}

	log.Info("successfully cleaned up polls")
	return nil
}
