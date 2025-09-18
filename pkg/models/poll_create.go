package models

import (
	"time"

	"github.com/goccy/go-json"
	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
)

func (m *PollModel) CreatePoll(r *plugnmeet.CreatePollReq) (string, error) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": r.RoomId,
		"userId": r.UserId,
		"method": "CreatePoll",
	})
	log.Infoln("request to create poll")

	r.PollId = uuid.NewString()
	log = log.WithField("pollId", r.PollId)

	// create poll hash and add to room
	err := m.createRoomPollHash(r)
	if err != nil {
		log.WithError(err).Errorln("failed to create room poll hash")
		return "", err
	}

	err = m.natsService.BroadcastSystemEventToEveryoneExceptUserId(plugnmeet.NatsMsgServerToClientEvents_POLL_CREATED, r.RoomId, r.PollId, r.UserId)
	if err != nil {
		log.WithError(err).Errorln("error sending POLL_CREATED event")
	}

	// send analytics
	toRecord := struct {
		PollId   string                         `json:"poll_id"`
		Question string                         `json:"question"`
		Options  []*plugnmeet.CreatePollOptions `json:"options"`
	}{
		PollId:   r.PollId,
		Question: r.Question,
		Options:  r.Options,
	}
	marshal, err := json.Marshal(toRecord)
	if err != nil {
		log.WithError(err).Errorln("failed to marshal analytics data")
	}
	val := string(marshal)
	m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_POLL_ADDED,
		RoomId:    r.RoomId,
		HsetValue: &val,
	})

	log.Info("successfully created poll")
	return r.PollId, nil
}

// createRoomPollHash will insert the poll to room hash
func (m *PollModel) createRoomPollHash(r *plugnmeet.CreatePollReq) error {
	p := &plugnmeet.PollInfo{
		Id:        r.PollId,
		RoomId:    r.RoomId,
		Question:  r.Question,
		Options:   r.Options,
		IsRunning: true,
		Created:   time.Now().Unix(),
		CreatedBy: r.UserId,
	}

	marshal, err := protojson.Marshal(p)
	if err != nil {
		return err
	}

	pollVal := map[string]string{
		r.PollId: string(marshal),
	}
	return m.rs.CreateRoomPoll(r.RoomId, pollVal)
}

func (m *PollModel) UserSubmitResponse(r *plugnmeet.SubmitPollResponseReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": r.RoomId,
		"userId": r.UserId,
		"pollId": r.PollId,
		"method": "UserSubmitResponse",
	})
	log.Infoln("request to submit poll response")

	err := m.rs.AddPollResponse(r)
	if err != nil {
		log.WithError(err).Errorln("failed to add poll response to redis")
		return err
	}

	// send analytics
	toRecord := struct {
		PollId         string `json:"poll_id"`
		SelectedOption uint64 `json:"selected_option"`
	}{
		PollId:         r.PollId,
		SelectedOption: r.SelectedOption,
	}
	marshal, err := json.Marshal(toRecord)
	if err != nil {
		log.WithError(err).Errorln("failed to marshal analytics data")
	}
	val := string(marshal)
	m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_USER,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_VOTED_POLL,
		RoomId:    r.RoomId,
		UserId:    &r.UserId,
		HsetValue: &val,
	})

	log.Info("successfully submitted poll response")
	return nil
}
