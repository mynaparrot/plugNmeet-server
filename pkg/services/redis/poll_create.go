package redisservice

import (
	"errors"
	"fmt"
	"github.com/goccy/go-json"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/redis/go-redis/v9"
	"strings"
)

func (s *RedisService) CreateRoomPoll(roomId string, val map[string]string) error {
	_, err := s.rc.HSet(s.ctx, pollsKey+roomId, val).Result()
	if err != nil {
		return err
	}
	return nil
}

func (s *RedisService) CreatePollResponseHash(roomId, pollId string, val map[string]interface{}) error {
	key := fmt.Sprintf("%s%s:respondents:%s", pollsKey, roomId, pollId)
	_, err := s.rc.HSet(s.ctx, key, val).Result()
	if err != nil {
		return err
	}
	return nil
}

type userResponseCommonFields struct {
	TotalRes       int    `redis:"total_resp"`
	AllRespondents string `redis:"all_respondents"`
}

func (s *RedisService) AddPollResponse(r *plugnmeet.SubmitPollResponseReq) error {
	key := fmt.Sprintf("%s%s:respondents:%s", pollsKey, r.RoomId, r.PollId)

	err := s.rc.Watch(s.ctx, func(tx *redis.Tx) error {
		d := new(userResponseCommonFields)
		err := tx.HMGet(s.ctx, key, "all_respondents").Scan(d)
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
		pp.HSet(s.ctx, key, map[string]string{
			"all_respondents": string(marshal),
		})
		pp.HIncrBy(s.ctx, key, "total_resp", 1)
		pp.HIncrBy(s.ctx, key, fmt.Sprintf("%d_count", r.SelectedOption), 1)
		_, err = pp.Exec(s.ctx)

		return err
	}, key)

	return err
}
