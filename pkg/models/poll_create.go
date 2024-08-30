package models

import (
	"fmt"
	"github.com/goccy/go-json"
	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	log "github.com/sirupsen/logrus"
	"time"
)

func (m *PollModel) CreatePoll(r *plugnmeet.CreatePollReq, isAdmin bool) (string, error) {
	r.PollId = uuid.NewString()

	// first add to room
	err := m.createRoomPollHash(r)
	if err != nil {
		return "", err
	}

	// now create empty respondent hash
	err = m.createRespondentHash(r)
	if err != nil {
		return "", err
	}

	err = m.natsService.BroadcastSystemEventToEveryoneExceptUserId(plugnmeet.NatsMsgServerToClientEvents_POLL_CREATED, r.RoomId, r.PollId, r.UserId)
	if err != nil {
		log.Errorln(err)
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
		log.Errorln(err)
	}
	val := string(marshal)
	m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_POLL_ADDED,
		RoomId:    r.RoomId,
		HsetValue: &val,
	})

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

	marshal, err := json.Marshal(p)
	if err != nil {
		return err
	}

	pollVal := map[string]string{
		r.PollId: string(marshal),
	}
	return m.rs.CreateRoomPoll(r.RoomId, pollVal)
}

// createRespondentHash will create initial hash
// format for all_respondents array value = userId:option_id
func (m *PollModel) createRespondentHash(r *plugnmeet.CreatePollReq) error {
	v := make(map[string]interface{})
	v["total_resp"] = 0
	v["all_respondents"] = nil

	for _, o := range r.Options {
		c := fmt.Sprintf("%d_count", o.Id)
		v[c] = 0
	}

	return m.rs.CreatePollResponseHash(r.RoomId, r.PollId, v)
}

func (m *PollModel) UserSubmitResponse(r *plugnmeet.SubmitPollResponseReq, isAdmin bool) error {
	err := m.rs.AddPollResponse(r)
	if err != nil {
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
		log.Errorln(err)
	}
	val := string(marshal)
	m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_USER,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_VOTED_POLL,
		RoomId:    r.RoomId,
		UserId:    &r.UserId,
		HsetValue: &val,
	})

	return nil
}
