package redisservice

import (
	"errors"
	"fmt"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/redis/go-redis/v9"
)

// session room keys are in the format: pnm:sessionData:{roomId}:{dataType}
const sessionDataPrefix = Prefix + "sessionData:"

// SaveSessionData stores an encrypted session-scoped blob (hash field key) and
// sets a 24h safety-net TTL on the whole hash. Value bytes are opaque to us.
func (s *RedisService) SaveSessionData(roomId string, dataType plugnmeet.SessionDataType, key string, value []byte) error {
	hashKey := fmt.Sprintf("%s%s:%s", sessionDataPrefix, roomId, dataType)

	pipe := s.rc.Pipeline()
	pipe.HSet(s.ctx, hashKey, key, value)
	pipe.Expire(s.ctx, hashKey, DefaultTTL)

	_, err := pipe.Exec(s.ctx)
	return err
}

// GetSessionData retrieves a single field from a session data hash.
// Returns nil, nil when the hash key or field does not exist.
func (s *RedisService) GetSessionData(roomId string, dataType plugnmeet.SessionDataType, key string) ([]byte, error) {
	hashKey := fmt.Sprintf("%s%s:%s", sessionDataPrefix, roomId, dataType)

	val, err := s.rc.HGet(s.ctx, hashKey, key).Bytes()
	switch {
	case errors.Is(err, redis.Nil):
		return nil, nil
	case err != nil:
		return nil, err
	}
	return val, nil
}

// GetAllSessionData retrieves the full contents of a session data hash.
func (s *RedisService) GetAllSessionData(roomId string, dataType plugnmeet.SessionDataType) (map[string]string, error) {
	hashKey := fmt.Sprintf("%s%s:%s", sessionDataPrefix, roomId, dataType)
	return s.rc.HGetAll(s.ctx, hashKey).Result()
}

// DeleteSessionRoomData removes all session data hashes for a room via a
// SCAN + DEL cursor loop (never KEYS).
func (s *RedisService) DeleteSessionRoomData(roomId string) error {
	pattern := fmt.Sprintf("%s%s:*", sessionDataPrefix, roomId)
	allKeys, err := s.ScanKeys(pattern)
	if err != nil {
		return err
	}
	if len(allKeys) == 0 {
		return nil
	}
	return s.DeleteKeys(allKeys)
}
