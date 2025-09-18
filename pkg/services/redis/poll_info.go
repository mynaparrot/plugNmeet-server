package redisservice

import (
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

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
