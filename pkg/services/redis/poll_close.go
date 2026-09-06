package redisservice

import (
	"errors"
	"fmt"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/encoding/protojson"
)

func (s *RedisService) ClosePoll(r *plugnmeet.ClosePollReq) error {
	// e.g. key: pnm:polls:{roomId}
	key := pollsKey + r.RoomId

	err := s.rc.Watch(s.ctx, func(tx *redis.Tx) error {
		g := tx.HGet(s.ctx, key, r.PollId)

		result, err := g.Result()
		switch {
		case errors.Is(err, redis.Nil):
			return config.ErrPollNotFound
		case err != nil:
			return err
		}
		if result == "" {
			return config.ErrPollNotFound
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

		_, err = tx.TxPipelined(s.ctx, func(pipe redis.Pipeliner) error {
			pipe.HSet(s.ctx, key, r.PollId, string(marshal))
			// every close path must purge the duration index hint
			pipe.HDel(s.ctx, pollsWithDurationKey, pollDurationIndexField(r.RoomId, r.PollId))
			return nil
		})

		return err
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
		// purge the room's duration index entries along with the polls
		pp.HDel(s.ctx, pollsWithDurationKey, pollDurationIndexField(roomId, id))
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

// ClosePollIfRunning atomically flips a running poll to closed with the given
// closed_by marker; returns true only when it made the running->closed flip.
func (s *RedisService) ClosePollIfRunning(roomId, pollId, closedBy string) (bool, error) {
	// e.g. key: pnm:polls:{roomId}
	key := pollsKey + roomId
	closed := false

	err := s.rc.Watch(s.ctx, func(tx *redis.Tx) error {
		closed = false // reset: the tx may be retried on conflicts

		result, err := tx.HGet(s.ctx, key, pollId).Result()
		switch {
		case errors.Is(err, redis.Nil):
			return nil // poll gone (e.g. room ended); nothing to close
		case err != nil:
			return err
		}

		info := new(plugnmeet.PollInfo)
		err = protojson.Unmarshal([]byte(result), info)
		if err != nil {
			return err
		}
		// already closed; keep the original closed_by untouched
		if !info.IsRunning {
			return nil
		}

		info.IsRunning = false
		info.ClosedBy = closedBy
		marshal, err := protojson.Marshal(info)
		if err != nil {
			return err
		}

		_, err = tx.TxPipelined(s.ctx, func(pipe redis.Pipeliner) error {
			pipe.HSet(s.ctx, key, pollId, string(marshal))
			// every close path must purge the duration index hint
			pipe.HDel(s.ctx, pollsWithDurationKey, pollDurationIndexField(roomId, pollId))
			return nil
		})
		if err != nil {
			return err
		}
		closed = true

		return nil
	}, key)

	return closed, err
}
