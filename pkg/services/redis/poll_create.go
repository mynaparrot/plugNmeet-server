package redisservice

import (
	"errors"
	"fmt"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/encoding/protojson"
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

// AddPollResponse records one vote: increments total_resp once per voter and
// each selected option counter once. Anonymous polls never touch all_respondents.
func (s *RedisService) AddPollResponse(r *plugnmeet.SubmitPollResponseReq, isAnonymous bool) error {
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
			return config.ErrPollAlreadyVoted
		}

		// All writes run in MULTI/EXEC so the WATCH above aborts on concurrent votes.
		_, err = tx.TxPipelined(s.ctx, func(pipe redis.Pipeliner) error {
			// Add user to the set of voters (keeps one-shot protection for anonymous polls too).
			pipe.SAdd(s.ctx, votedUsersKey, r.UserId)
			pipe.Expire(s.ctx, votedUsersKey, time.Hour*24)

			// Anonymous polls: no per-user attribution anywhere, counters only.
			if !isAnonymous {
				// format userId:option_id(:option_id...):name — comma-joined ids for multi-select
				voteData := fmt.Sprintf("%s:%s:%s", r.UserId, config.JoinPollOptionIds(r.SelectedOptions), r.Name)

				// Add the vote details to a list.
				pipe.RPush(s.ctx, allRespondentsKey, voteData)
				pipe.Expire(s.ctx, allRespondentsKey, time.Hour*24)
			}

			// total_resp counts distinct voters; each selected option counts once.
			pipe.HIncrBy(s.ctx, respondentsKey, PollTotalRespField, 1)
			for _, id := range r.SelectedOptions {
				pipe.HIncrBy(s.ctx, respondentsKey, fmt.Sprintf("%d%s", id, PollCountSuffix), 1)
			}
			pipe.Expire(s.ctx, respondentsKey, time.Hour*24)

			return nil
		})

		return err
	}, votedUsersKey)
}

// ReopenPollIfClosed atomically flips a closed poll back to running, keeping
// all responses/counters/voted_users; returns the duration and whether the
// closed->running flip actually happened.
func (s *RedisService) ReopenPollIfClosed(r *plugnmeet.ReopenPollReq) (uint32, bool, error) {
	// e.g. key: pnm:polls:{roomId}
	key := pollsKey + r.RoomId
	reopened := false
	duration := uint32(0)

	err := s.rc.Watch(s.ctx, func(tx *redis.Tx) error {
		reopened = false // reset: the tx may be retried on conflicts
		duration = 0

		result, err := tx.HGet(s.ctx, key, r.PollId).Result()
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
		// idempotent: already running — keep everything untouched
		if info.IsRunning {
			return nil
		}

		info.IsRunning = true
		info.ClosedBy = ""
		// restart the expiry from now; 0 means no limit
		info.ExpiresAt = 0
		if info.Duration > 0 {
			info.ExpiresAt = time.Now().Unix() + int64(info.Duration)
		}
		marshal, err := protojson.Marshal(info)
		if err != nil {
			return err
		}

		_, err = tx.TxPipelined(s.ctx, func(pipe redis.Pipeliner) error {
			pipe.HSet(s.ctx, key, r.PollId, string(marshal))
			return nil
		})
		if err != nil {
			return err
		}
		reopened = true
		duration = info.Duration

		return nil
	}, key)

	return duration, reopened, err
}
