package redisservice

import (
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	EtherpadRoomsPrefix = Prefix + "etherpad_rooms:" // A SET for each nodeId
	EtherpadTokenPrefix = Prefix + "etherpad_token:" // A STRING for each nodeId
)

// formatEtherpadRoomsKey generates the Redis key for the set of active rooms on a node.
func (s *RedisService) formatEtherpadRoomsKey(nodeId string) string {
	return fmt.Sprintf("%s%s", EtherpadRoomsPrefix, nodeId)
}

// formatEtherpadTokenKey generates the Redis key for an Etherpad token.
func (s *RedisService) formatEtherpadTokenKey(nodeId string) string {
	return fmt.Sprintf("%s%s", EtherpadTokenPrefix, nodeId)
}

// AddEtherpadRoom adds a room to the set of active rooms for a specific etherpad node.
func (s *RedisService) AddEtherpadRoom(nodeId, roomId string) error {
	key := s.formatEtherpadRoomsKey(nodeId)
	return s.rc.SAdd(s.ctx, key, roomId).Err()
}

// GetEtherpadActiveRoomsCount counts how many rooms are active on a specific etherpad node.
func (s *RedisService) GetEtherpadActiveRoomsCount(nodeId string) (int64, error) {
	key := s.formatEtherpadRoomsKey(nodeId)
	return s.rc.SCard(s.ctx, key).Result()
}

// RemoveEtherpadRoom removes a room from the set of active rooms for a node.
func (s *RedisService) RemoveEtherpadRoom(nodeId, roomId string) error {
	key := s.formatEtherpadRoomsKey(nodeId)
	return s.rc.SRem(s.ctx, key, roomId).Err()
}

// AddEtherpadToken stores a temporary access token in Redis with a specific TTL.
func (s *RedisService) AddEtherpadToken(nodeId, token string, expiration time.Duration) error {
	key := s.formatEtherpadTokenKey(nodeId)
	return s.rc.Set(s.ctx, key, token, expiration).Err()
}

// GetEtherpadToken retrieves a temporary access token from Redis.
func (s *RedisService) GetEtherpadToken(nodeId string) (string, error) {
	key := s.formatEtherpadTokenKey(nodeId)
	val, err := s.rc.Get(s.ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", nil // Key not found, not an error
		}
		return "", err
	}
	return val, nil
}
