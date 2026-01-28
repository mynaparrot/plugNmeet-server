package redisservice

import (
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

const breakoutRoomHashKey = Prefix + "breakoutRoom:%s"

// formatBreakoutRoomHashKey generates the Redis key for the hash that stores all breakout rooms for a parent room.
func (s *RedisService) formatBreakoutRoomHashKey(parentRoomId string) string {
	return fmt.Sprintf(breakoutRoomHashKey, parentRoomId)
}

// InsertOrUpdateBreakoutRoom adds or updates a breakout room in the parent room's hash.
// It also sets a TTL on the entire hash, mirroring the NATS KV bucket behavior.
func (s *RedisService) InsertOrUpdateBreakoutRoom(parentRoomId, bkRoomId string, val []byte) error {
	key := s.formatBreakoutRoomHashKey(parentRoomId)
	pipe := s.rc.Pipeline()
	pipe.HSet(s.ctx, key, bkRoomId, val)
	pipe.Expire(s.ctx, key, DefaultTTL)
	_, err := pipe.Exec(s.ctx)
	return err
}

// DeleteBreakoutRoom removes a specific breakout room (a field) from the parent room's hash.
func (s *RedisService) DeleteBreakoutRoom(parentRoomId, bkRoomId string) error {
	key := s.formatBreakoutRoomHashKey(parentRoomId)
	return s.rc.HDel(s.ctx, key, bkRoomId).Err()
}

// GetBreakoutRoom retrieves the data for a specific breakout room from the parent room's hash.
func (s *RedisService) GetBreakoutRoom(parentRoomId, bkRoomId string) (string, error) {
	key := s.formatBreakoutRoomHashKey(parentRoomId)
	val, err := s.rc.HGet(s.ctx, key, bkRoomId).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", nil // Field or key not found, not an error.
		}
		return "", err
	}
	return val, nil
}

// CountBreakoutRooms returns the number of breakout rooms (fields) in the parent room's hash.
func (s *RedisService) CountBreakoutRooms(parentRoomId string) (int64, error) {
	key := s.formatBreakoutRoomHashKey(parentRoomId)
	return s.rc.HLen(s.ctx, key).Result()
}

// GetAllBreakoutRoomsByParentRoomId retrieves all breakout rooms (all fields and values)
// from the parent room's hash.
func (s *RedisService) GetAllBreakoutRoomsByParentRoomId(parentRoomId string) (map[string]string, error) {
	key := s.formatBreakoutRoomHashKey(parentRoomId)
	result, err := s.rc.HGetAll(s.ctx, key).Result()
	if err != nil {
		return nil, err
	}

	return result, nil
}

// DeleteAllBreakoutRoomsByParentRoomId deletes the entire hash for the parent room.
func (s *RedisService) DeleteAllBreakoutRoomsByParentRoomId(parentRoomId string) error {
	key := s.formatBreakoutRoomHashKey(parentRoomId)
	return s.rc.Del(s.ctx, key).Err()
}

// GetBreakoutRoomIdsByParentRoomId retrieves the IDs of all breakout rooms (all fields)
// from the parent room's hash.
func (s *RedisService) GetBreakoutRoomIdsByParentRoomId(parentRoomId string) ([]string, error) {
	key := s.formatBreakoutRoomHashKey(parentRoomId)
	return s.rc.HKeys(s.ctx, key).Result()
}
