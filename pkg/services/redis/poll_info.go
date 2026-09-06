package redisservice

import (
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

func (s *RedisService) GetAllRoomPolls(roomId string) (map[string]string, error) {
	// e.g. key: pnm:polls:{roomId}
	result, err := s.rc.HGetAll(s.ctx, pollsKey+roomId).Result()
	switch {
	case errors.Is(err, redis.Nil):
		return nil, nil
	case err != nil:
		return nil, err
	}

	return result, nil
}

func (s *RedisService) GetPollsListByRoomId(roomId string) ([]string, error) {
	// e.g. key: pnm:polls:{roomId}
	result, err := s.rc.HVals(s.ctx, pollsKey+roomId).Result()
	switch {
	case errors.Is(err, redis.Nil):
		return nil, nil
	case err != nil:
		return nil, err
	}

	return result, nil
}

func (s *RedisService) GetPollIdsByRoomId(roomId string) ([]string, error) {
	// e.g. key: pnm:polls:{roomId}
	result, err := s.rc.HKeys(s.ctx, pollsKey+roomId).Result()
	switch {
	case errors.Is(err, redis.Nil):
		return nil, nil
	case err != nil:
		return nil, err
	}

	return result, nil
}

func (s *RedisService) GetPollAllRespondents(roomId, pollId string) ([]string, error) {
	// e.g. key: pnm:polls:{roomId}:respondents:{pollId}:all_respondents
	key := fmt.Sprintf("%s%s%s%s%s", pollsKey, roomId, pollRespondentsSubKey, pollId, pollAllResSubKey)
	result, err := s.rc.LRange(s.ctx, key, 0, -1).Result()

	switch {
	case errors.Is(err, redis.Nil):
		return nil, nil
	case err != nil:
		return nil, err
	}

	return result, nil
}

// IsUserVotedPoll checks voted_users SET membership without touching all_respondents
// (safe for anonymous polls where no per-user attribution exists).
func (s *RedisService) IsUserVotedPoll(roomId, pollId, userId string) (bool, error) {
	// e.g. pnm:polls:{roomId}:respondents:{pollId}:voted_users
	key := fmt.Sprintf("%s%s%s%s%s", pollsKey, roomId, pollRespondentsSubKey, pollId, pollVotedUsersSubKey)
	result, err := s.rc.SIsMember(s.ctx, key, userId).Result()

	switch {
	case errors.Is(err, redis.Nil):
		return false, nil
	case err != nil:
		return false, err
	}

	return result, nil
}

func (s *RedisService) GetPollCountersByPollId(roomId, pollId string) (map[string]string, error) {
	// e.g. key: pnm:polls:{roomId}:respondents:{pollId}
	key := fmt.Sprintf("%s%s%s%s", pollsKey, roomId, pollRespondentsSubKey, pollId)
	result, err := s.rc.HGetAll(s.ctx, key).Result()

	switch {
	case errors.Is(err, redis.Nil):
		return nil, nil
	case err != nil:
		return nil, err
	}

	return result, nil
}

func (s *RedisService) GetPollTotalResponses(roomId, pollId string) (string, error) {
	// e.g. key: pnm:polls:{roomId}:respondents:{pollId}
	key := fmt.Sprintf("%s%s%s%s", pollsKey, roomId, pollRespondentsSubKey, pollId)
	result, err := s.rc.HGet(s.ctx, key, PollTotalRespField).Result()

	switch {
	case errors.Is(err, redis.Nil):
		return "0", nil
	case err != nil:
		return "", err
	}

	return result, nil
}

func (s *RedisService) GetPollInfoByPollId(roomId, pollId string) (string, error) {
	// e.g. key: pnm:polls:{roomId}
	result, err := s.rc.HGet(s.ctx, pollsKey+roomId, pollId).Result()

	switch {
	case errors.Is(err, redis.Nil):
		return "", nil
	case err != nil:
		return "", err
	}

	return result, nil
}
