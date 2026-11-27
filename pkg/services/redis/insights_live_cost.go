package redisservice

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"
)

const (
	// InsightsActiveRoomsKey is the set of rooms that currently accrue insights
	// usage, so a live cost reader knows what to look at.
	InsightsActiveRoomsKey = Prefix + "insights:active_rooms"
	// InsightsLiveCostKey is the per-room hash of service field -> cost.
	InsightsLiveCostKey = Prefix + "insights:live_cost:%s"

	LiveCostFieldTranscription   = "transcription"
	LiveCostFieldTTS             = "tts"
	LiveCostFieldChatTranslation = "chat_translation"
	LiveCostFieldAITextChat      = "ai_text_chat"
	LiveCostFieldAIAgent         = "ai_agent"
	LiveCostFieldAIAgentAvatar   = "ai_agent_avatar"
)

func liveCostKey(roomId string) string {
	return fmt.Sprintf(InsightsLiveCostKey, roomId)
}

func formatLiveCost(cost float64) string {
	return strconv.FormatFloat(cost, 'f', 6, 64)
}

// SetInsightsLiveCost overwrites the live cost of one service field.
func (s *RedisService) SetInsightsLiveCost(ctx context.Context, roomId, field string, cost float64) error {
	costKey := liveCostKey(roomId)
	pipe := s.rc.TxPipeline()
	pipe.HSet(ctx, costKey, field, formatLiveCost(cost))
	pipe.Expire(ctx, costKey, s.defaultTTL)
	pipe.SAdd(ctx, InsightsActiveRoomsKey, roomId)
	pipe.Expire(ctx, InsightsActiveRoomsKey, s.defaultTTL)
	_, err := pipe.Exec(ctx)
	return err
}

// IncrInsightsLiveCost adds delta to a service field. A non-positive delta books
// no cost but still keeps the room registered.
func (s *RedisService) IncrInsightsLiveCost(ctx context.Context, roomId, field string, delta float64) error {
	if delta < 0 {
		return nil
	}

	costKey := liveCostKey(roomId)
	pipe := s.rc.TxPipeline()
	if delta > 0 {
		pipe.HIncrByFloat(ctx, costKey, field, delta)
		pipe.Expire(ctx, costKey, s.defaultTTL)
	}
	pipe.SAdd(ctx, InsightsActiveRoomsKey, roomId)
	pipe.Expire(ctx, InsightsActiveRoomsKey, s.defaultTTL)
	_, err := pipe.Exec(ctx)
	return err
}

// GetInsightsLiveCost returns the live cost of every service of the room.
func (s *RedisService) GetInsightsLiveCost(ctx context.Context, roomId string) (map[string]float64, error) {
	raw, err := s.rc.HGetAll(ctx, liveCostKey(roomId)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return map[string]float64{}, nil
		}
		return nil, err
	}

	costs := make(map[string]float64, len(raw))
	for field, value := range raw {
		cost, err := strconv.ParseFloat(value, 64)
		if err != nil {
			s.logger.WithError(err).Warnf("could not parse live cost value '%s' for field '%s'", value, field)
			continue
		}
		costs[field] = cost
	}
	return costs, nil
}

// CleanupInsightsLiveCost drops the room's live cost once it has been harvested.
func (s *RedisService) CleanupInsightsLiveCost(ctx context.Context, roomId string) error {
	pipe := s.rc.TxPipeline()
	pipe.Del(ctx, liveCostKey(roomId))
	pipe.SRem(ctx, InsightsActiveRoomsKey, roomId)
	_, err := pipe.Exec(ctx)
	return err
}
