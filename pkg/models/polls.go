package models

import (
	"context"
	"errors"
	"fmt"
	"github.com/go-redis/redis/v8"
	"github.com/goccy/go-json"
	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"strconv"
	"strings"
	"time"
)

const pollsKey = "pnm:polls:"

type PollInfo struct {
	Id        string              `json:"id"`
	RoomId    string              `json:"roomId"`
	Question  string              `json:"question"`
	Options   []CreatePollOptions `json:"options"`
	IsRunning bool                `json:"is_running"`
	Created   int64               `json:"created"`
	CreatedBy string              `json:"created_by"`
	ClosedBy  string              `json:"closed_by"`
}

type newPollsModel struct {
	rc  *redis.Client
	ctx context.Context
}

func NewPollsModel() *newPollsModel {
	return &newPollsModel{
		rc:  config.AppCnf.RDS,
		ctx: context.Background(),
	}
}

type CreatePollReq struct {
	RoomId   string
	UserId   string
	PollId   string
	Question string              `json:"question" validate:"required"`
	Options  []CreatePollOptions `json:"options" validate:"required"`
}

type CreatePollOptions struct {
	Id   int    `json:"id" validate:"required"`
	Text string `json:"text" validate:"required"`
}

func (m *newPollsModel) CreatePoll(r *CreatePollReq, isAdmin bool) (error, string) {
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

	return nil, r.PollId
}

// addPollToRoom will insert poll to room hash
func (m *newPollsModel) addPollToRoom(r *CreatePollReq) error {
	p := PollInfo{
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
func (m *newPollsModel) createRespondentHash(r *CreatePollReq) error {
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

func (m *newPollsModel) ListPolls(roomId string) (error, []*PollInfo) {
	var polls []*PollInfo

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
		info := new(PollInfo)
		err = json.Unmarshal([]byte(pi), info)
		if err != nil {
			continue
		}

		polls = append(polls, info)
	}

	return nil, polls
}

func (m *newPollsModel) GetPollResponsesByField(roomId, pollId, field string) (error, string) {
	key := fmt.Sprintf("%s%s:respondents:%s", pollsKey, roomId, pollId)

	v := m.rc.HGet(m.ctx, key, field)
	result, err := v.Result()

	return err, result
}

func (m *newPollsModel) UserSelectedOption(roomId, pollId, userId string) (error, int) {
	err, allRespondents := m.GetPollResponsesByField(roomId, pollId, "all_respondents")
	if err != nil {
		return err, 0
	}

	if allRespondents == "" {
		return err, 0
	}

	var respondents []string
	err = json.Unmarshal([]byte(allRespondents), &respondents)
	if err != nil {
		return err, 0
	}

	for i := 0; i < len(respondents); i++ {
		// format userId:option_id:name
		p := strings.Split(respondents[i], ":")
		if p[0] == userId {
			voted, err := strconv.Atoi(p[1])
			if err != nil {
				return err, 0
			}
			return err, voted
		}
	}

	return nil, 0
}

type UserSubmitResponseReq struct {
	RoomId         string
	PollId         string `json:"poll_id" validate:"required"`
	UserId         string `json:"user_id" validate:"required"`
	Name           string `json:"name" validate:"required"`
	SelectedOption int    `json:"selected_option" validate:"required"`
}

type userResponseCommonFields struct {
	TotalRes       int    `redis:"total_resp"`
	AllRespondents string `redis:"all_respondents"`
}

func (m *newPollsModel) UserSubmitResponse(r *UserSubmitResponseReq, isAdmin bool) error {
	key := fmt.Sprintf("%s%s:respondents:%s", pollsKey, r.RoomId, r.PollId)

	err := m.rc.Watch(m.ctx, func(tx *redis.Tx) error {
		d := new(userResponseCommonFields)
		v := tx.HMGet(m.ctx, key, "all_respondents")
		err := v.Scan(d)
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

	return nil
}

func (m *newPollsModel) broadcastNotification(roomId, userId, pollId string, mType plugnmeet.DataMsgBodyType, isAdmin bool) error {
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

type ClosePollReq struct {
	RoomId string
	UserId string
	PollId string `json:"poll_id" validate:"required"`
}

func (m *newPollsModel) ClosePoll(r *ClosePollReq, isAdmin bool) error {
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

		info := new(PollInfo)
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
		tx.HMSet(m.ctx, key, pollVal)

		return nil
	}, key)

	if err != nil {
		return err
	}

	_ = m.broadcastNotification(r.RoomId, r.UserId, r.PollId, plugnmeet.DataMsgBodyType_POLL_CLOSED, isAdmin)

	return nil
}

func (m *newPollsModel) CleanUpPolls(roomId string) error {
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
		return err
	}

	return nil
}

func (m *newPollsModel) GetPollResponsesDetails(roomId, pollId string) (error, map[string]string) {
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

type ResponsesResultRes struct {
	Question       string                   `json:"question"`
	TotalResponses int                      `json:"total_responses"`
	Options        []ResponsesResultOptions `json:"options"`
}

type ResponsesResultOptions struct {
	Id        int    `json:"id"`
	Text      string `json:"text"`
	VoteCount int    `json:"vote_count"`
}

func (m *newPollsModel) GetResponsesResult(roomId, pollId string) (*ResponsesResultRes, error) {
	res := new(ResponsesResultRes)

	p := m.rc.HGet(m.ctx, pollsKey+roomId, pollId)
	pi, err := p.Result()
	if err != nil {
		return nil, err
	}

	info := new(PollInfo)
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

	var options []ResponsesResultOptions
	for _, opt := range info.Options {
		f := fmt.Sprintf("%d_count", opt.Id)
		i, _ := strconv.Atoi(result[f])
		rr := ResponsesResultOptions{
			Id:        opt.Id,
			Text:      opt.Text,
			VoteCount: i,
		}
		options = append(options, rr)
	}

	res.Options = options
	i, _ := strconv.Atoi(result["total_resp"])
	res.TotalResponses = i

	return res, nil
}

type PollsStatsRes struct {
	TotalPolls   int `json:"total_polls"`
	TotalRunning int `json:"total_running"`
}

func (m *newPollsModel) GetPollsStats(roomId string) (*PollsStatsRes, error) {
	res := new(PollsStatsRes)

	p := m.rc.HGetAll(m.ctx, pollsKey+roomId)
	result, err := p.Result()
	if err != nil {
		return nil, err
	}

	if len(result) == 0 {
		// no polls
		return res, nil
	}
	res.TotalPolls = len(result)

	for _, pi := range result {
		info := new(PollInfo)
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
