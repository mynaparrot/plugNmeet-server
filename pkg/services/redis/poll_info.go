package redisservice

import (
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

const pollsKey = "pnm:polls:"

func (s *RedisService) GetPollsListByRoomId(roomId string) (map[string]string, error) {
	result, err := s.rc.HGetAll(s.ctx, pollsKey+roomId).Result()
	switch {
	case errors.Is(err, redis.Nil):
		return nil, nil
	case err != nil:
		return nil, err
	}

	return result, nil
}

func (s *RedisService) GetPollResponsesByField(roomId, pollId, field string) (string, error) {
	key := fmt.Sprintf("%s%s:respondents:%s", pollsKey, roomId, pollId)
	result, err := s.rc.HGet(s.ctx, key, field).Result()

	switch {
	case errors.Is(err, redis.Nil):
		return "", nil
	case err != nil:
		return "", err
	}

	return result, nil
}

func (s *RedisService) GetPollResponsesByPollId(roomId, pollId string) (map[string]string, error) {
	key := fmt.Sprintf("%s%s:respondents:%s", pollsKey, roomId, pollId)
	result, err := s.rc.HGetAll(s.ctx, key).Result()

	switch {
	case errors.Is(err, redis.Nil):
		return nil, nil
	case err != nil:
		return nil, err
	}

	return result, nil
}

func (s *RedisService) GetPollInfoByPollId(roomId, pollId string) (string, error) {
	result, err := s.rc.HGet(s.ctx, pollsKey+roomId, pollId).Result()

	switch {
	case errors.Is(err, redis.Nil):
		return "", nil
	case err != nil:
		return "", err
	}

	return result, nil
}
