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
	TranscriptionSessionsKey = "pnm:insights:transcription_sessions:%s"
	TranscriptionUsageKey    = "pnm:insights:transcription_usage:%s"
)

// HandleTranscriptionUsage manages the lifecycle of a user's transcription session.
func (s *RedisService) HandleTranscriptionUsage(ctx context.Context, roomId, userId string, isStarted bool) (int64, error) {
	sessionsKey := fmt.Sprintf(TranscriptionSessionsKey, roomId)
	usageKey := fmt.Sprintf(TranscriptionUsageKey, roomId)

	if isStarted {
		pipe := s.rc.TxPipeline()
		pipe.HSet(ctx, sessionsKey, userId, time.Now().Unix())
		pipe.Expire(ctx, sessionsKey, 24*time.Hour)
		_, err := pipe.Exec(ctx)
		if err != nil {
			return 0, err
		}
		return 0, nil
	}

	startTimeStr, err := s.rc.HGet(ctx, sessionsKey, userId).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return 0, nil
		}
		return 0, err
	}

	if err := s.rc.HDel(ctx, sessionsKey, userId).Err(); err != nil {
		s.logger.WithError(err).Error("failed to delete active transcription session")
	}

	startTime, err := strconv.ParseInt(startTimeStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("could not parse start time '%s': %w", startTimeStr, err)
	}

	duration := time.Now().Unix() - startTime
	if duration < 0 {
		duration = 0
	}

	pipe := s.rc.TxPipeline()
	pipe.HIncrBy(ctx, usageKey, userId, duration)
	pipe.HIncrBy(ctx, usageKey, "total_usage", duration)
	pipe.Expire(ctx, usageKey, 24*time.Hour)
	_, err = pipe.Exec(ctx)

	if err != nil {
		return 0, err
	}

	return duration, nil
}

// GetTranscriptionUserUsage retrieves the transcription usage for a single user.
func (s *RedisService) GetTranscriptionUserUsage(ctx context.Context, roomId, userId string) (int64, error) {
	key := fmt.Sprintf(TranscriptionUsageKey, roomId)
	res, err := s.rc.HGet(ctx, key, userId).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return 0, nil
		}
		return 0, err
	}
	return strconv.ParseInt(res, 10, 64)
}

// GetTranscriptionRoomUsage retrieves all transcription usage data for a room.
// If cleanup is true, it deletes the key after retrieval.
func (s *RedisService) GetTranscriptionRoomUsage(ctx context.Context, roomId string, cleanup bool) (map[string]int64, error) {
	key := fmt.Sprintf(TranscriptionUsageKey, roomId)
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
			s.logger.WithError(err).Warnf("could not parse transcription usage value '%s' for key '%s'", v, k)
			continue
		}
		usageMap[k] = val
	}

	return usageMap, nil
}
