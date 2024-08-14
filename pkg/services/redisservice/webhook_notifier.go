package redisservice

import (
	"errors"
	"github.com/redis/go-redis/v9"
)

const (
	WebhookRedisKey = "pnm:webhookData"
)

func (s *RedisService) AddWebhookData(roomId string, val []byte) error {
	_, err := s.rc.HSet(s.ctx, WebhookRedisKey, roomId, val).Result()
	if err != nil {
		return err
	}
	return nil
}

func (s *RedisService) GetWebhookData(roomId string) (string, error) {
	result, err := s.rc.HGet(s.ctx, WebhookRedisKey, roomId).Result()
	switch {
	case errors.Is(err, redis.Nil):
		return "", nil
	case err != nil:
		return "", err
	}

	return result, nil
}

func (s *RedisService) DeleteWebhookData(roomId string) error {
	_, err := s.rc.HDel(s.ctx, WebhookRedisKey, roomId).Result()
	if err != nil {
		return err
	}
	return nil
}
