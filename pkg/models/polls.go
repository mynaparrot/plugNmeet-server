package models

import (
	"context"
	"errors"
	"fmt"
	"github.com/goccy/go-json"
	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
	"strconv"
	"strings"
	"time"
)

const pollsKey = "pnm:polls:"

type PollsModel struct {
	rc             *redis.Client
	ctx            context.Context
	analyticsModel *AnalyticsModel
}

func NewPollsModel() *PollsModel {
	return &PollsModel{
		rc:             config.AppCnf.RDS,
		ctx:            context.Background(),
		analyticsModel: NewAnalyticsModel(),
	}
}

func (m *PollsModel) CreatePoll(r *plugnmeet.CreatePollReq, isAdmin bool) (error, string) {
	r.PollId = uuid.NewString()

	// first add to room
	err := m.addPollToRoom(r)
	if err != nil {
		return err, ""
	}

	// now create empty respondent hash
	err = m.createRespondentHash(r)
	if err != nil {
		return err, ""
	}

	_ = m.broadcastNotification(r.RoomId, r.UserId, r.PollId, plugnmeet.DataMsgBodyType_POLL_CREATED, isAdmin)

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

	return nil, r.PollId
}

// addPollToRoom will insert poll to room hash
func (m *PollsModel) addPollToRoom(r *plugnmeet.CreatePollReq) error {
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

	pp := m.rc.Pipeline()
	pp.HSet(m.ctx, pollsKey+r.RoomId, pollVal)
	_, err = pp.Exec(m.ctx)

	return err
}

// createRespondentHash will create initial hash
// format for all_respondents array value = userId:option_id
func (m *PollsModel) createRespondentHash(r *plugnmeet.CreatePollReq) error {
	key := fmt.Sprintf("%s%s:respondents:%s", pollsKey, r.RoomId, r.PollId)

	v := make(map[string]interface{})
	v["total_resp"] = 0
	v["all_respondents"] = nil

	for _, o := range r.Options {
		c := fmt.Sprintf("%d_count", o.Id)
		v[c] = 0
	}

	pp := m.rc.Pipeline()
	pp.HSet(m.ctx, key, v)
	_, err := pp.Exec(m.ctx)

	return err
}

func (m *PollsModel) ListPolls(roomId string) (error, []*plugnmeet.PollInfo) {
	var polls []*plugnmeet.PollInfo

	p := m.rc.HGetAll(m.ctx, pollsKey+roomId)
	result, err := p.Result()
	if err != nil {
		return err, nil
	}

	if len(result) == 0 {
		// no polls
		return nil, polls
	}

	for _, pi := range result {
		info := new(plugnmeet.PollInfo)
		err = json.Unmarshal([]byte(pi), info)
		if err != nil {
			continue
		}

		polls = append(polls, info)
	}

	return nil, polls
}

func (m *PollsModel) GetPollResponsesByField(roomId, pollId, field string) (error, string) {
	key := fmt.Sprintf("%s%s:respondents:%s", pollsKey, roomId, pollId)

	v := m.rc.HGet(m.ctx, key, field)
	result, err := v.Result()

	return err, result
}

func (m *PollsModel) UserSelectedOption(roomId, pollId, userId string) (uint64, error) {
	err, allRespondents := m.GetPollResponsesByField(roomId, pollId, "all_respondents")
	if err != nil {
		return 0, err
	}

	if allRespondents == "" {
		return 0, err
	}

	var respondents []string
	err = json.Unmarshal([]byte(allRespondents), &respondents)
	if err != nil {
		return 0, err
	}

	for i := 0; i < len(respondents); i++ {
		// format userId:option_id:name
		p := strings.Split(respondents[i], ":")
		if p[0] == userId {
			voted, err := strconv.ParseUint(p[1], 10, 64)
			if err != nil {
				return 0, err
			}
			return voted, err
		}
	}

	return 0, nil
}

type userResponseCommonFields struct {
	TotalRes       int    `redis:"total_resp"`
	AllRespondents string `redis:"all_respondents"`
}

func (m *PollsModel) UserSubmitResponse(r *plugnmeet.SubmitPollResponseReq, isAdmin bool) error {
	key := fmt.Sprintf("%s%s:respondents:%s", pollsKey, r.RoomId, r.PollId)

	err := m.rc.Watch(m.ctx, func(tx *redis.Tx) error {
		d := new(userResponseCommonFields)
		err := tx.HMGet(m.ctx, key, "all_respondents").Scan(d)
		if err != nil {
			return err
		}

		var respondents []string
		if d.AllRespondents != "" {
			err = json.Unmarshal([]byte(d.AllRespondents), &respondents)
			if err != nil {
				return err
			}
		}

		if len(respondents) > 0 {
			for i := 0; i < len(respondents); i++ {
				// format userId:option_id:name
				p := strings.Split(respondents[i], ":")
				if p[0] == r.UserId {
					return errors.New("user already voted")
				}
			}
		}

		// format userId:option_id:name
		respondents = append(respondents, fmt.Sprintf("%s:%d:%s", r.UserId, r.SelectedOption, r.Name))
		marshal, err := json.Marshal(respondents)
		if err != nil {
			return err
		}

		pp := tx.Pipeline()
		pp.HSet(m.ctx, key, map[string]string{
			"all_respondents": string(marshal),
		})
		pp.HIncrBy(m.ctx, key, "total_resp", 1)
		pp.HIncrBy(m.ctx, key, fmt.Sprintf("%d_count", r.SelectedOption), 1)
		_, err = pp.Exec(m.ctx)

		return err
	}, key)

	if err != nil {
		return err
	}

	_ = m.broadcastNotification(r.RoomId, r.UserId, r.PollId, plugnmeet.DataMsgBodyType_NEW_POLL_RESPONSE, isAdmin)

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

func (m *PollsModel) broadcastNotification(roomId, userId, pollId string, mType plugnmeet.DataMsgBodyType, isAdmin bool) error {
	payload := &plugnmeet.DataMessage{
		Type:   plugnmeet.DataMsgType_SYSTEM,
		RoomId: roomId,
		Body: &plugnmeet.DataMsgBody{
			Type: mType,
			From: &plugnmeet.DataMsgReqFrom{
				UserId: userId,
			},
			Msg: pollId,
		},
	}

	msg := &WebsocketToRedis{
		Type:    "sendMsg",
		DataMsg: payload,
		RoomId:  roomId,
		IsAdmin: isAdmin,
	}
	DistributeWebsocketMsgToRedisChannel(msg)

	return nil
}

func (m *PollsModel) ClosePoll(r *plugnmeet.ClosePollReq, isAdmin bool) error {
	key := pollsKey + r.RoomId

	err := m.rc.Watch(m.ctx, func(tx *redis.Tx) error {
		g := tx.HGet(m.ctx, key, r.PollId)

		result, err := g.Result()
		if err != nil {
			return err
		}
		if result == "" {
			return errors.New("not found")
		}

		info := new(plugnmeet.PollInfo)
		err = json.Unmarshal([]byte(result), info)
		if err != nil {
			return err
		}

		info.IsRunning = false
		info.ClosedBy = r.UserId
		marshal, err := json.Marshal(info)
		if err != nil {
			return err
		}

		pollVal := map[string]string{
			r.PollId: string(marshal),
		}
		tx.HSet(m.ctx, key, pollVal)

		return nil
	}, key)

	if err != nil {
		return err
	}

	_ = m.broadcastNotification(r.RoomId, r.UserId, r.PollId, plugnmeet.DataMsgBodyType_POLL_CLOSED, isAdmin)

	// send analytics
	m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_POLL_ENDED,
		RoomId:    r.RoomId,
		HsetValue: &r.PollId,
	})

	return nil
}

func (m *PollsModel) CleanUpPolls(roomId string) error {
	err, polls := m.ListPolls(roomId)
	if err != nil {
		return err
	}
	pp := m.rc.Pipeline()

	for _, p := range polls {
		key := fmt.Sprintf("%s%s:respondents:%s", pollsKey, roomId, p.Id)
		pp.Del(m.ctx, key)
	}

	roomKey := pollsKey + roomId
	pp.Del(m.ctx, roomKey)

	_, err = pp.Exec(m.ctx)
	if err != nil {
		log.Errorln(err)
		return err
	}

	return nil
}

func (m *PollsModel) GetPollResponsesDetails(roomId, pollId string) (error, map[string]string) {
	key := fmt.Sprintf("%s%s:respondents:%s", pollsKey, roomId, pollId)
	var result map[string]string

	err := m.rc.Watch(m.ctx, func(tx *redis.Tx) error {
		v := tx.HGetAll(m.ctx, key)
		r, err := v.Result()
		result = r
		return err
	}, key)

	if len(result) < 0 {
		return nil, nil
	}

	return err, result
}

func (m *PollsModel) GetResponsesResult(roomId, pollId string) (*plugnmeet.PollResponsesResult, error) {
	res := new(plugnmeet.PollResponsesResult)

	p := m.rc.HGet(m.ctx, pollsKey+roomId, pollId)
	pi, err := p.Result()
	if err != nil {
		return nil, err
	}

	info := new(plugnmeet.PollInfo)
	err = json.Unmarshal([]byte(pi), info)
	if err != nil {
		return nil, err
	}
	if info.IsRunning {
		return nil, errors.New("need to wait until poll close")
	}
	res.Question = info.Question

	key := fmt.Sprintf("%s%s:respondents:%s", pollsKey, roomId, pollId)
	c := m.rc.HGetAll(m.ctx, key)
	result, err := c.Result()
	if err != nil {
		return nil, err
	}

	var options []*plugnmeet.PollResponsesResultOptions
	for _, opt := range info.Options {
		f := fmt.Sprintf("%d_count", opt.Id)
		i, _ := strconv.Atoi(result[f])
		rr := &plugnmeet.PollResponsesResultOptions{
			Id:        uint64(opt.Id),
			Text:      opt.Text,
			VoteCount: uint64(i),
		}
		options = append(options, rr)
	}

	res.Options = options
	i, _ := strconv.Atoi(result["total_resp"])
	res.TotalResponses = uint64(i)

	return res, nil
}

func (m *PollsModel) GetPollsStats(roomId string) (*plugnmeet.PollsStats, error) {
	res := &plugnmeet.PollsStats{
		TotalPolls:   0,
		TotalRunning: 0,
	}

	p := m.rc.HGetAll(m.ctx, pollsKey+roomId)
	result, err := p.Result()
	if err != nil {
		return nil, err
	}

	if len(result) == 0 {
		// no polls
		return nil, nil
	}
	res.TotalPolls = uint64(len(result))

	for _, pi := range result {
		info := new(plugnmeet.PollInfo)
		err = json.Unmarshal([]byte(pi), info)
		if err != nil {
			continue
		}

		if info.IsRunning {
			res.TotalRunning += 1
		}
	}

	return res, nil
}
