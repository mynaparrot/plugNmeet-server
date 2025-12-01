package redisservice

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	TTSServiceRedisKey = Prefix + "insights:ttsService"
)

func (s *RedisService) UpdateTTSServiceUsage(ctx context.Context, roomId, userId, language string, incBy int) error {
	key := fmt.Sprintf("%s:%s:usage", TTSServiceRedisKey, roomId)
	pipe := s.rc.TxPipeline()
	pipe.HIncrBy(ctx, key, userId, int64(incBy))
	pipe.HIncrBy(ctx, key, language, int64(incBy))
	pipe.HIncrBy(ctx, key, "total_usage", int64(incBy))
	pipe.Expire(ctx, key, time.Hour*24)
	_, err := pipe.Exec(ctx)
	return err
}

// GetTTSServiceUserUsage retrieves the TTS usage for a single user.
func (s *RedisService) GetTTSServiceUserUsage(ctx context.Context, roomId, userId string) (int64, error) {
	key := fmt.Sprintf("%s:%s:usage", TTSServiceRedisKey, roomId)
	res, err := s.rc.HGet(ctx, key, userId).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return 0, nil
		}
		return 0, err
	}
	return strconv.ParseInt(res, 10, 64)
}

// GetTTSServiceRoomUsage retrieves all TTS usage data for a room.
// If cleanup is true, it deletes the key after retrieval.
func (s *RedisService) GetTTSServiceRoomUsage(ctx context.Context, roomId string, cleanup bool) (map[string]int64, error) {
	key := fmt.Sprintf("%s:%s:usage", TTSServiceRedisKey, roomId)
	var res *redis.MapStringStringCmd

	_, err := s.rc.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		res = pipe.HGetAll(ctx, key)
		if cleanup {
			pipe.Del(ctx, key)
		}
		return nil
	})

	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}

	rawMap, err := res.Result()
	if err != nil {
		return nil, err
	}

	usageMap := make(map[string]int64, len(rawMap))
	for k, v := range rawMap {
		val, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			s.logger.WithError(err).Warnf("could not parse TTS usage value '%s' for key '%s'", v, k)
			continue
		}
		usageMap[k] = val
	}

	return usageMap, nil
}
