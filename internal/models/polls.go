package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/mynaparrot/plugNmeet/internal/config"
	"strconv"
	"strings"
	"time"
)

const pollsKey = "pnm:polls:"

type PollInfo struct {
	Id          string              `json:"id"`
	RoomId      string              `json:"roomId"`
	Question    string              `json:"question"`
	Options     []CreatePollOptions `json:"options"`
	IsPublished bool                `json:"is_published"`
	Created     int64               `json:"created"`
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
	PollId   string
	Question string              `json:"question" validate:"required"`
	Options  []CreatePollOptions `json:"options" validate:"required"`
}

type CreatePollOptions struct {
	Id   int    `json:"id" validate:"required"`
	Text string `json:"text" validate:"required"`
}

func (m *newPollsModel) CreatePoll(r *CreatePollReq) error {
	r.PollId = uuid.NewString()

	// first add to room
	err := m.addPollToRoom(r)
	if err != nil {
		return err
	}

	// now create empty respondent hash
	err = m.createRespondentHash(r)
	return err
}

// addPollToRoom will insert poll to room hash
func (m *newPollsModel) addPollToRoom(r *CreatePollReq) error {
	p := PollInfo{
		Id:          r.PollId,
		RoomId:      r.RoomId,
		Question:    r.Question,
		Options:     r.Options,
		IsPublished: false,
		Created:     time.Now().Unix(),
	}

	marshal, err := json.Marshal(p)
	if err != nil {
		return err
	}

	pollVal := map[string]string{
		r.PollId: string(marshal),
	}

	err = m.rc.Watch(m.ctx, func(tx *redis.Tx) error {
		pp := tx.Pipeline()

		pp.HSet(m.ctx, pollsKey+r.RoomId, pollVal)
		_, err = pp.Exec(m.ctx)

		return err
	}, pollsKey+r.RoomId)

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

type ListPoll struct {
	Id             string `json:"id"`
	Question       string `json:"question"`
	IsPublished    bool   `json:"is_published"`
	TotalResponses int    `json:"total_responses"`
	Voted          int    `json:"voted"`
}

func (m *newPollsModel) ListPolls(roomId, userId string) (error, []*ListPoll) {
	var result map[string]string
	var polls []*ListPoll

	err := m.rc.Watch(m.ctx, func(tx *redis.Tx) error {
		p := tx.HGetAll(m.ctx, pollsKey+roomId)
		r, err := p.Result()
		if err != nil {
			return err
		}
		result = r

		return err
	}, pollsKey+roomId)

	if err != nil {
		return err, polls
	}

	if len(result) == 0 {
		// no polls
		return nil, polls
	}

	for _, p := range result {
		info := new(PollInfo)
		err = json.Unmarshal([]byte(p), info)
		if err != nil {
			continue
		}
		err, total, userRes := m.getUserResponseWithTotal(roomId, userId, info.Id)
		if err != nil {
			continue
		}

		poll := ListPoll{
			Id:             info.Id,
			Question:       info.Question,
			IsPublished:    info.IsPublished,
			TotalResponses: total,
			Voted:          userRes,
		}
		polls = append(polls, &poll)
	}

	return nil, polls
}

func (m *newPollsModel) getPollResponsesByField(roomId, pollId, field string) (error, string) {
	key := fmt.Sprintf("%s%s:respondents:%s", pollsKey, roomId, pollId)
	var result string

	err := m.rc.Watch(m.ctx, func(tx *redis.Tx) error {
		v := tx.HGet(m.ctx, key, field)
		r, err := v.Result()
		result = r
		return err
	}, key)

	return err, result
}

type UserResponseWithTotal struct {
	TotalResponses       int `json:"total_responses"`
	UserResponseOptionId int `json:"user_response_option_id"`
}

type userResponseWithTotal struct {
	TotalRes       int    `redis:"total_resp"`
	AllRespondents string `redis:"all_respondents"`
}

func (m *newPollsModel) getUserResponseWithTotal(roomId, userId, pollId string) (error, int, int) {
	key := fmt.Sprintf("%s%s:respondents:%s", pollsKey, roomId, pollId)
	result := new(userResponseWithTotal)
	fields := []string{"total_resp", "all_respondents"}

	err := m.rc.Watch(m.ctx, func(tx *redis.Tx) error {
		v := tx.HMGet(m.ctx, key, fields...)
		err := v.Scan(result)
		return err
	}, key)

	if err != nil {
		return err, 0, 0
	}

	if result.AllRespondents == "" {
		return nil, result.TotalRes, 0
	}

	var respondents []string
	err = json.Unmarshal([]byte(result.AllRespondents), &respondents)
	if err != nil {
		return err, result.TotalRes, 0
	}

	for _, rr := range respondents {
		// format userId:option_id
		p := strings.Split(rr, ":")
		if p[0] == userId {
			voted, _ := strconv.Atoi(p[1])
			return nil, result.TotalRes, voted
		}
	}

	return nil, result.TotalRes, 0
}

func (m *newPollsModel) GetPollResponses(roomId, pollId string) (error, string) {
	key := fmt.Sprintf("%s%s:respondents:%s", pollsKey, roomId, pollId)
	var result map[string]string

	err := m.rc.Watch(m.ctx, func(tx *redis.Tx) error {
		v := tx.HGetAll(m.ctx, key)
		r, err := v.Result()
		result = r
		return err
	}, key)

	if len(result) < 0 {
		return nil, ""
	}

	marshal, err := json.Marshal(result)
	if err != nil {
		return err, ""
	}

	return err, string(marshal)
}

type UserSubmitResponseReq struct {
	RoomId         string
	PollId         string `json:"poll_id" validate:"required"`
	UserId         string `json:"user_id" validate:"required"`
	SelectedOption int    `json:"selected_option" validate:"required"`
}

func (m *newPollsModel) UserSubmitResponse(r *UserSubmitResponseReq) error {
	key := fmt.Sprintf("%s%s:respondents:%s", pollsKey, r.RoomId, r.PollId)

	err := m.rc.Watch(m.ctx, func(tx *redis.Tx) error {
		d := new(userResponseWithTotal)
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
				if strings.Contains(respondents[i], r.UserId) {
					return errors.New("user already voted")
				}
			}
		}

		// format userId:option_id
		respondents = append(respondents, fmt.Sprintf("%s:%d", r.UserId, r.SelectedOption))
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

	return err
}
