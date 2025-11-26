package redisservice

import (
	"context"
	"fmt"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	aiTextChatRedisKey = "pnm:insights:aiTextChat"
)

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
