package redisservice

import (
	"errors"

	"github.com/redis/go-redis/v9"
)

const (
	WebhookHashKey        = Prefix + "webhookData"    // A single HASH key for all webhook data
	WebhookCleanupSubject = Prefix + "webhookCleanup" // pub/sub subject
)

// AddWebhookData adds or updates webhook data for a room and sets a TTL on that specific hash field.
func (s *RedisService) AddWebhookData(roomId string, val []byte) error {
	key := WebhookHashKey
	pipe := s.rc.Pipeline()
	// Set the roomId as a field in the hash
	pipe.HSet(s.ctx, key, roomId, val)
	pipe.HExpire(s.ctx, key, DefaultTTL, roomId)
	_, err := pipe.Exec(s.ctx)
	return err
}

// GetWebhookData retrieves webhook data for a specific room from the main webhook hash.
func (s *RedisService) GetWebhookData(roomId string) ([]byte, error) {
	key := WebhookHashKey
	val, err := s.rc.HGet(s.ctx, key, roomId).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			// Key (hash itself) or field (roomId) not found
			return nil, nil
		}
		return nil, err
	}
	return []byte(val), nil
}

// DeleteWebhookData deletes the webhook data for a specific room (removes a field from the hash).
func (s *RedisService) DeleteWebhookData(roomId string) error {
	key := WebhookHashKey
	// HDel removes the field (roomId) from the hash
	return s.rc.HDel(s.ctx, key, roomId).Err()
}
