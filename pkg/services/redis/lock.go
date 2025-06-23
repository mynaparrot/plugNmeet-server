package redisservice

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
	"time"
)

const (
	RoomCreationLockKey = Prefix + "roomCreationLock-%s"
	SchedulerLockKey    = Prefix + "schedulerLock-%s"
)

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

	// Lua script for atomic check-and-delete.
	script := `
    if redis.call("GET", KEYS[1]) == ARGV[1] then
        return redis.call("DEL", KEYS[1])
    else
        return 0 
    end
    `
	deleted, err := s.rc.Eval(ctx, script, []string{key}, lockValue).Int64()
	if errors.Is(err, redis.Nil) {
		// Key didn't exist, which is fine (lock expired or already released).
		return nil
	}
	if err != nil {
		return fmt.Errorf("redis Eval error for unlock script on key %s (roomID: %s): %w", key, roomID, err)
	}

	if deleted == 0 {
		log.Warnf("UnlockRoomCreation: Lock for roomID %s not held by this instance (lockValue: %s) or lock expired before unlock.", roomID, lockValue)
	} else {
		log.Infof("UnlockRoomCreation: Successfully released lock for roomID %s (lockValue: %s)", roomID, lockValue)
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

func (s *RedisService) LockSchedulerTask(task string, ttl time.Duration) error {
	key := fmt.Sprintf(SchedulerLockKey, task)
	_, err := s.rc.Set(s.ctx, key, fmt.Sprintf("%d", time.Now().Unix()), ttl).Result()
	if err != nil {
		return err
	}

	return nil
}

func (s *RedisService) IsSchedulerTaskLock(task string) bool {
	key := fmt.Sprintf(SchedulerLockKey, task)
	result, err := s.rc.Get(s.ctx, key).Result()
	if err != nil {
		return false
	}

	if result != "" {
		return true
	}

	return false
}

func (s *RedisService) UnlockSchedulerTask(task string) {
	_, _ = s.rc.Del(s.ctx, fmt.Sprintf(SchedulerLockKey, task)).Result()
}
