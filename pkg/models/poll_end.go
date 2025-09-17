package models

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

func (m *PollModel) ClosePoll(r *plugnmeet.ClosePollReq) error {
	err := m.rs.ClosePoll(r)
	if err != nil {
		return err
	}

	err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_POLL_CLOSED, r.RoomId, r.PollId, nil)
	if err != nil {
		m.logger.WithError(err).Errorln("error sending POLL_CLOSED event")
	}

	// send analytics
	m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_POLL_ENDED,
		RoomId:    r.RoomId,
		HsetValue: &r.PollId,
	})

	return nil
}

func (m *PollModel) CleanUpPolls(roomId string) error {
	polls, err := m.ListPolls(roomId)
	if err != nil {
		return err
	}

	var pIds []string
	for _, p := range polls {
		pIds = append(pIds, p.Id)
	}

	return m.rs.CleanUpPolls(roomId, pIds)
}
