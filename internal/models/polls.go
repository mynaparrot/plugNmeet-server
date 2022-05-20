package models

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/mynaparrot/plugNmeet/internal/config"
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
		tx.Unwatch(m.ctx, pollsKey+r.RoomId)

		return err
	}, pollsKey+r.RoomId)

	return err
}

func (m *newPollsModel) createRespondentHash(r *CreatePollReq) error {
	v := make(map[string]interface{})
	key := fmt.Sprintf("%s%s:respondents:%s", pollsKey, r.RoomId, r.PollId)
	for _, o := range r.Options {
		c := fmt.Sprintf("%d_count", o.Id)
		res := fmt.Sprintf("%d_respondents", o.Id)
		v[c] = 0
		v[res] = nil
	}

	pp := m.rc.Pipeline()
	pp.HSet(m.ctx, key, v)
	_, err := pp.Exec(m.ctx)

	return err
}
