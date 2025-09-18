package redisservice

import (
	"errors"
	"fmt"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/encoding/protojson"
)

func (s *RedisService) ClosePoll(r *plugnmeet.ClosePollReq) error {
	// e.g. key: pnm:polls:{roomId}
	key := pollsKey + r.RoomId

	err := s.rc.Watch(s.ctx, func(tx *redis.Tx) error {
		g := tx.HGet(s.ctx, key, r.PollId)

		result, err := g.Result()
		if err != nil {
			return err
		}
		if result == "" {
			return errors.New("not found")
		}

		info := new(plugnmeet.PollInfo)
		err = protojson.Unmarshal([]byte(result), info)
		if err != nil {
			return err
		}

		info.IsRunning = false
		info.ClosedBy = r.UserId
		marshal, err := protojson.Marshal(info)
		if err != nil {
			return err
		}

		tx.HSet(s.ctx, key, r.PollId, string(marshal))

		return nil
	}, key)

	return err
}

func (s *RedisService) CleanUpPolls(roomId string, pollIds []string) error {
	pp := s.rc.Pipeline()

	for _, id := range pollIds {
		// e.g. pnm:polls:{roomId}:respondents:{pollId}
		respondentsKey := fmt.Sprintf("%s%s%s%s", pollsKey, roomId, pollRespondentsSubKey, id)
		// e.g. pnm:polls:{roomId}:respondents:{pollId}:voted_users
		votedUsersKey := fmt.Sprintf("%s%s", respondentsKey, pollVotedUsersSubKey)
		// e.g. pnm:polls:{roomId}:respondents:{pollId}:all_respondents
		allRespondentsKey := fmt.Sprintf("%s%s", respondentsKey, pollAllResSubKey)
		pp.Del(s.ctx, respondentsKey)
		pp.Del(s.ctx, votedUsersKey)
		pp.Del(s.ctx, allRespondentsKey)
	}

	// e.g. pnm:polls:{roomId}
	roomKey := pollsKey + roomId
	pp.Del(s.ctx, roomKey)

	_, err := pp.Exec(s.ctx)
	if err != nil {
		return err
	}

	return nil
}
