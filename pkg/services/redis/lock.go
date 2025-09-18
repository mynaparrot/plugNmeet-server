package redisservice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	RoomCreationLockKey = Prefix + "roomCreationLock-%s"
	janitorLockKey      = Prefix + "janitorLock-%s"
)

// unlockScript is a Lua script for atomic check-and-delete.
const unlockScript = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
else
    return 0
end
`

// LockRoomCreation attempts to acquire a distributed lock.
// Returns:
// - acquired (bool): true if the lock was acquired.
// - lockValue (string): A unique value if acquired, to be used for safe unlocking. Empty if not acquired.
// - err (error): For Redis communication errors.
func (s *RedisService) LockRoomCreation(ctx context.Context, roomID string, ttl time.Duration) (acquired bool, lockValue string, err error) {
	key := fmt.Sprintf(RoomCreationLockKey, roomID)
	val := uuid.New().String() // Unique value for this lock instance

	// Atomically SET the key if it Not eXists (NX), with a TTL.
	ok, err := s.rc.SetNX(ctx, key, val, ttl).Result()
	if err != nil {
		return false, "", fmt.Errorf("redis SetNX error for key %s: %w", key, err)
	}

	if !ok {
		return false, "", nil // Lock not acquired (already held)
	}

	return true, val, nil // Lock acquired
}

// UnlockRoomCreation safely releases a lock using the lockValue.
func (s *RedisService) UnlockRoomCreation(ctx context.Context, roomID string, lockValue string) error {
	key := fmt.Sprintf(RoomCreationLockKey, roomID)
	if lockValue == "" {
		// This can happen if we attempt to unlock a lock that was never acquired
		// or if the lockValue was not properly propagated.
		return nil // Or return an error if this state is unexpected
	}

	deleted, err := s.unlockScriptExec.Eval(ctx, s.rc, []string{key}, lockValue).Int64()
	if errors.Is(err, redis.Nil) {
		// Key didn't exist, which is fine (lock expired or already released).
		return nil
	}
	if err != nil {
		return fmt.Errorf("redis Eval error for unlock script on key %s (roomID: %s): %w", key, roomID, err)
	}

	if deleted == 0 {
		return fmt.Errorf("could not release lock on key %s roomID: %s (it may have expired or been taken by another process)", key, roomID)
	}
	return nil
}

// IsRoomCreationLock checks if the room creation lock key exists in Redis.
// Returns true if locked, false if not locked.
// Returns an error for Redis communication issues.
func (s *RedisService) IsRoomCreationLock(ctx context.Context, roomID string) (isLocked bool, err error) {
	key := fmt.Sprintf(RoomCreationLockKey, roomID)
	val, err := s.rc.Exists(ctx, key).Result() // EXISTS returns 1 if key exists, 0 if not.
	if err != nil {
		return false, fmt.Errorf("redis Exists error for key %s: %w", key, err)
	}
	return val == 1, nil
}

func (s *RedisService) IsJanitorTaskLock(task string) bool {
	val, _ := s.rc.Get(s.ctx, fmt.Sprintf(janitorLockKey, task)).Result()
	return val != ""
}

func (s *RedisService) LockJanitorTask(task string, duration time.Duration) {
	err := s.rc.Set(s.ctx, fmt.Sprintf(janitorLockKey, task), "locked", duration).Err()
	if err != nil {
		s.logger.WithError(err).Errorln("LockJanitorTask failed")
	}
}

func (s *RedisService) UnlockJanitorTask(task string) {
	_, _ = s.rc.Del(s.ctx, fmt.Sprintf(janitorLockKey, task)).Result()
}
