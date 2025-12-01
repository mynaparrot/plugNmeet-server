package redisservice

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	TTSServiceRedisKey       = Prefix + "ttsService"
	ChatTranslationRedisKey  = Prefix + "chatTranslationService"
	aiTextChatRedisKey       = Prefix + "insights:aiTextChat"
	transcriptionSessionsKey = "pnm:insights:transcription_sessions:%s"
	transcriptionUsageKey    = "pnm:insights:transcription_usage:%s"
	roomTotalField           = "_room_total_"
)

// HandleTranscriptionUsage manages the lifecycle of a user's transcription session.
// If 'isStarted' is true, it records the session start time.
// If 'isStarted' is false, it calculates the duration and updates usage totals.
func (s *RedisService) HandleTranscriptionUsage(ctx context.Context, roomId, userId string, isStarted bool) (int64, error) {
	sessionsKey := fmt.Sprintf(transcriptionSessionsKey, roomId)
	usageKey := fmt.Sprintf(transcriptionUsageKey, roomId)

	if isStarted {
		// START logic
		pipe := s.rc.TxPipeline()
		pipe.HSet(ctx, sessionsKey, userId, time.Now().Unix())
		pipe.Expire(ctx, sessionsKey, 48*time.Hour)
		_, err := pipe.Exec(ctx)
		if err != nil {
			return 0, err
		}
		return 0, nil
	}

	// END logic
	// 1. Get the start time from the active sessions hash.
	startTimeStr, err := s.rc.HGet(ctx, sessionsKey, userId).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			// Session not found, maybe already processed or never started.
			return 0, nil
		}
		return 0, err
	}

	// 2. Immediately remove the session from the active list.
	if err := s.rc.HDel(ctx, sessionsKey, userId).Err(); err != nil {
		s.logger.WithError(err).Error("failed to delete active transcription session")
	}

	startTime, err := strconv.ParseInt(startTimeStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("could not parse start time '%s': %w", startTimeStr, err)
	}

	// 3. Calculate the duration.
	duration := time.Now().Unix() - startTime
	if duration < 0 {
		duration = 0
	}

	// 4. Use a pipeline to efficiently update both usage counters.
	pipe := s.rc.TxPipeline()
	pipe.HIncrBy(ctx, usageKey, userId, duration)
	pipe.HIncrBy(ctx, usageKey, roomTotalField, duration)
	pipe.Expire(ctx, usageKey, 48*time.Hour) // Also set an expiration on the usage data
	_, err = pipe.Exec(ctx)

	if err != nil {
		return 0, err
	}

	return duration, nil
}

func (s *RedisService) UpdateChatTranslationUsage(ctx context.Context, roomId, userId string, incBy int) error {
	key := fmt.Sprintf("%s:%s:usage", ChatTranslationRedisKey, roomId)
	pipe := s.rc.TxPipeline()
	pipe.HIncrBy(ctx, key, userId, int64(incBy))
	pipe.HIncrBy(ctx, key, "total_usage", int64(incBy))
	pipe.Expire(ctx, key, time.Hour*24)
	_, err := pipe.Exec(ctx)
	return err
}

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

func (s *RedisService) GetAITextChatSummary(ctx context.Context, roomId, userId string) (string, error) {
	key := fmt.Sprintf("%s:summary:%s:%s", aiTextChatRedisKey, roomId, userId)
	return s.rc.Get(ctx, key).Result()
}

func (s *RedisService) GetAITextChatContext(ctx context.Context, roomId, userId string, start, stop int64) ([]*plugnmeet.InsightsAITextChatContent, error) {
	key := fmt.Sprintf("%s:context:%s:%s", aiTextChatRedisKey, roomId, userId)
	res, err := s.rc.LRange(ctx, key, start, stop).Result()
	if err != nil {
		return nil, err
	}

	var content []*plugnmeet.InsightsAITextChatContent
	for _, r := range res {
		c := new(plugnmeet.InsightsAITextChatContent)
		err = protojson.Unmarshal([]byte(r), c)
		if err != nil {
			continue
		}
		content = append(content, c)
	}

	return content, nil
}

func (s *RedisService) SetAITextChatSummary(ctx context.Context, roomId, userId, summary string) error {
	key := fmt.Sprintf("%s:summary:%s:%s", aiTextChatRedisKey, roomId, userId)
	return s.rc.Set(ctx, key, summary, 24*time.Hour).Err()
}

func (s *RedisService) AppendToAITextChatContext(ctx context.Context, roomId, userId string, messages ...*plugnmeet.InsightsAITextChatContent) error {
	key := fmt.Sprintf("%s:context:%s:%s", aiTextChatRedisKey, roomId, userId)
	pipe := s.rc.TxPipeline()

	for _, msg := range messages {
		val, err := protojson.Marshal(msg)
		if err != nil {
			continue
		}
		pipe.RPush(ctx, key, val)
	}
	pipe.Expire(ctx, key, 24*time.Hour)
	_, err := pipe.Exec(ctx)
	return err
}

func (s *RedisService) DeleteAITextChatContext(ctx context.Context, roomId, userId string) error {
	key := fmt.Sprintf("%s:context:%s:%s", aiTextChatRedisKey, roomId, userId)
	return s.rc.Del(ctx, key).Err()
}

func (s *RedisService) GetAITextChatContextLength(ctx context.Context, roomId, userId string) (int64, error) {
	key := fmt.Sprintf("%s:context:%s:%s", aiTextChatRedisKey, roomId, userId)
	return s.rc.LLen(ctx, key).Result()
}

func (s *RedisService) UpdateAITextChatUsage(ctx context.Context, roomId, userId string, taskType insights.AITaskType, promptTokens, completionTokens, totalTokens uint32) error {
	key := fmt.Sprintf("%s:usage:%s", aiTextChatRedisKey, roomId)
	pipe := s.rc.TxPipeline()

	// Per-user, per-task tracking
	userPromptKey := fmt.Sprintf("%s:%s:prompt", userId, taskType)
	userCompletionKey := fmt.Sprintf("%s:%s:completion", userId, taskType)
	userTotalKey := fmt.Sprintf("%s:%s:total", userId, taskType)

	pipe.HIncrBy(ctx, key, userPromptKey, int64(promptTokens))
	pipe.HIncrBy(ctx, key, userCompletionKey, int64(completionTokens))
	pipe.HIncrBy(ctx, key, userTotalKey, int64(totalTokens))

	// Global, per-task tracking
	totalPromptKey := fmt.Sprintf("total_%s_prompt_tokens", taskType)
	totalCompletionKey := fmt.Sprintf("total_%s_completion_tokens", taskType)
	totalTokensKey := fmt.Sprintf("total_%s_tokens", taskType)

	pipe.HIncrBy(ctx, key, totalPromptKey, int64(promptTokens))
	pipe.HIncrBy(ctx, key, totalCompletionKey, int64(completionTokens))
	pipe.HIncrBy(ctx, key, totalTokensKey, int64(totalTokens))

	pipe.Expire(ctx, key, 24*time.Hour)
	_, err := pipe.Exec(ctx)
	return err
}
