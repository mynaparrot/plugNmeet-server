package redisservice

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	AiTextChatRedisKey = Prefix + "insights:aiTextChat"

	aiTextChatUsageKey   = AiTextChatRedisKey + ":usage:%s"
	aiTextChatContextKey = AiTextChatRedisKey + ":context:%s:%s"
	aiTextChatSummaryKey = AiTextChatRedisKey + ":summary:%s:%s"

	AiTextChatTotalPromptTokenFields     = "total_%s_prompt_tokens"
	AiTextChatTotalCompletionTokenFields = "total_%s_completion_tokens"
	AiTextChatTotalTokenFields           = "total_%s_tokens"
)

func (s *RedisService) GetAITextChatSummary(ctx context.Context, roomId, userId string) (string, error) {
	key := fmt.Sprintf(aiTextChatSummaryKey, roomId, userId)
	return s.rc.Get(ctx, key).Result()
}

func (s *RedisService) GetAITextChatContext(ctx context.Context, roomId, userId string, start, stop int64) ([]*plugnmeet.InsightsAITextChatContent, error) {
	key := fmt.Sprintf(aiTextChatContextKey, roomId, userId)
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
	key := fmt.Sprintf(aiTextChatSummaryKey, roomId, userId)
	return s.rc.Set(ctx, key, summary, 24*time.Hour).Err()
}

func (s *RedisService) AppendToAITextChatContext(ctx context.Context, roomId, userId string, messages ...*plugnmeet.InsightsAITextChatContent) error {
	key := fmt.Sprintf(aiTextChatContextKey, roomId, userId)
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
	key := fmt.Sprintf(aiTextChatContextKey, roomId, userId)
	return s.rc.Del(ctx, key).Err()
}

func (s *RedisService) GetAITextChatContextLength(ctx context.Context, roomId, userId string) (int64, error) {
	key := fmt.Sprintf(aiTextChatContextKey, roomId, userId)
	return s.rc.LLen(ctx, key).Result()
}

func (s *RedisService) UpdateAITextChatUsage(ctx context.Context, roomId, userId string, taskType insights.AITaskType, promptTokens, completionTokens, totalTokens uint32) error {
	key := fmt.Sprintf(aiTextChatUsageKey, roomId)
	pipe := s.rc.TxPipeline()

	// Per-user, per-task tracking
	userPromptKey := fmt.Sprintf("%s:%s:prompt", userId, taskType)
	userCompletionKey := fmt.Sprintf("%s:%s:completion", userId, taskType)
	userTotalKey := fmt.Sprintf("%s:%s:total", userId, taskType)

	pipe.HIncrBy(ctx, key, userPromptKey, int64(promptTokens))
	pipe.HIncrBy(ctx, key, userCompletionKey, int64(completionTokens))
	pipe.HIncrBy(ctx, key, userTotalKey, int64(totalTokens))

	// Global, per-task tracking
	totalPromptKey := fmt.Sprintf(AiTextChatTotalPromptTokenFields, taskType)
	totalCompletionKey := fmt.Sprintf(AiTextChatTotalCompletionTokenFields, taskType)
	totalTokensKey := fmt.Sprintf(AiTextChatTotalTokenFields, taskType)

	pipe.HIncrBy(ctx, key, totalPromptKey, int64(promptTokens))
	pipe.HIncrBy(ctx, key, totalCompletionKey, int64(completionTokens))
	pipe.HIncrBy(ctx, key, totalTokensKey, int64(totalTokens))

	pipe.Expire(ctx, key, 24*time.Hour)
	_, err := pipe.Exec(ctx)
	return err
}

// GetAITextChatRoomUsage retrieves all AI text chat token usage for a room.
// If cleanup is true, it deletes the key after retrieval, including user-specific context and summary keys.
func (s *RedisService) GetAITextChatRoomUsage(ctx context.Context, roomId string, cleanup bool) (map[string]int64, error) {
	usageHashKey := fmt.Sprintf(aiTextChatUsageKey, roomId)

	rawMap, err := s.rc.HGetAll(ctx, usageHashKey).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}

	usageMap := make(map[string]int64, len(rawMap))
	userIds := make(map[string]struct{}) // To store unique user IDs for cleanup

	for k, v := range rawMap {
		val, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			s.logger.WithError(err).Warnf("could not parse AI chat usage value '%s' for key '%s'", v, k)
			continue
		}
		usageMap[k] = val

		// Extract userId from keys like "userId:taskType:prompt"
		if !strings.HasPrefix(k, "total_") {
			parts := strings.Split(k, ":")
			if len(parts) > 0 {
				userIds[parts[0]] = struct{}{}
			}
		}
	}

	if cleanup && len(usageMap) > 0 {
		pipe := s.rc.TxPipeline()
		pipe.Del(ctx, usageHashKey)

		for userId := range userIds {
			contextKey := fmt.Sprintf(aiTextChatContextKey, roomId, userId)
			pipe.Del(ctx, contextKey)

			summaryKey := fmt.Sprintf(aiTextChatSummaryKey, roomId, userId)
			pipe.Del(ctx, summaryKey)
		}

		_, err := pipe.Exec(ctx)
		if err != nil {
			s.logger.WithError(err).Errorf("failed to clean up AI text chat Redis keys for room %s", roomId)
		}
	}

	return usageMap, nil
}
