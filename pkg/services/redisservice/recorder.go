package redisservice

import (
	"errors"
	"github.com/redis/go-redis/v9"
)

const (
	RecorderChannel = "plug-n-meet-recorder"
	RecordersKey    = Prefix + "recorders"
)

func (s *RedisService) PublishToRecorderChannel(payload string) error {
	_, err := s.rc.Publish(s.ctx, RecorderChannel, payload).Result()
	if err != nil {
		return err
	}
	return nil
}

func (s *RedisService) GetAllRecorders() (map[string]string, error) {
	result, err := s.rc.HGetAll(s.ctx, RecordersKey).Result()
	switch {
	case errors.Is(err, redis.Nil):
		return nil, nil
	case err != nil:
		return nil, err
	}

	return result, nil
}
