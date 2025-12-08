package redisservice

import (
	"errors"
	"fmt"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/redis/go-redis/v9"
)

const (
	pollsKey              = "pnm:polls:"
	pollRespondentsSubKey = ":respondents:"
	pollVotedUsersSubKey  = ":voted_users"
	pollAllResSubKey      = ":all_respondents"
	PollTotalRespField    = "total_resp"
	PollCountSuffix       = "_count"
)

func (s *RedisService) CreateRoomPoll(roomId string, val map[string]string) error {
	pipe := s.rc.Pipeline()
	pipe.HSet(s.ctx, pollsKey+roomId, val)
	pipe.Expire(s.ctx, pollsKey+roomId, time.Hour*24)

	_, err := pipe.Exec(s.ctx)
	if err != nil {
		return err
	}
	return nil
}

func (s *RedisService) AddPollResponse(r *plugnmeet.SubmitPollResponseReq) error {
	// respondentsKey is the base key for a specific poll's responses.
	// It's a HASH that stores counters like total_resp, 1_count, etc.
	// e.g. pnm:polls:room_id:respondents:poll_id
	respondentsKey := fmt.Sprintf("%s%s%s%s", pollsKey, r.RoomId, pollRespondentsSubKey, r.PollId)

	// votedUsersKey is a SET that stores the user IDs of everyone who has voted.
	// Used for O(1) check to see if a user has already voted.
	// e.g. pnm:polls:room_id:respondents:poll_id:voted_users
	votedUsersKey := fmt.Sprintf("%s%s", respondentsKey, pollVotedUsersSubKey)

	// allRespondentsKey is a LIST that stores the detailed vote information for each user.
	// e.g. pnm:polls:room_id:respondents:poll_id:all_respondents
	allRespondentsKey := fmt.Sprintf("%s%s", respondentsKey, pollAllResSubKey)

	return s.rc.Watch(s.ctx, func(tx *redis.Tx) error {
		// Check if the user has already voted using a Set for O(1) lookup.
		voted, err := tx.SIsMember(s.ctx, votedUsersKey, r.UserId).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return err
		}
		if voted {
			return fmt.Errorf("user already voted")
		}

		// format userId:option_id:name
		voteData := fmt.Sprintf("%s:%d:%s", r.UserId, r.SelectedOption, r.Name)

		// Queue commands directly on the transaction object.
		// Add user to the set of voters.
		tx.SAdd(s.ctx, votedUsersKey, r.UserId)
		tx.Expire(s.ctx, votedUsersKey, time.Hour*24)

		// Add the vote details to a list.
		tx.RPush(s.ctx, allRespondentsKey, voteData)
		tx.Expire(s.ctx, allRespondentsKey, time.Hour*24)

		// Increment the total response counter.
		tx.HIncrBy(s.ctx, respondentsKey, PollTotalRespField, 1)
		// Increment the specific option counter.
		tx.HIncrBy(s.ctx, respondentsKey, fmt.Sprintf("%d%s", r.SelectedOption, PollCountSuffix), 1)
		tx.Expire(s.ctx, respondentsKey, time.Hour*24)
		// The commands will be executed when the function returns.

		return nil
	}, votedUsersKey)
}
