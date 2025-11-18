package redisservice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

const (
	RoomCreationLockKey = Prefix + "roomCreationLock-%s"
	janitorLockKey      = Prefix + "janitorLeaderLock"
)

// unlockScript is a Lua script for atomic check-and-delete.
const unlockScript = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
else
    return 0
end
`

// renewScript is a Lua script for atomic check-and-set TTL.
const renewScript = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("EXPIRE", KEYS[1], ARGV[2])
else
    return 0
end`

// Lock represents a single distributed lock instance, created by the RedisService.
type Lock struct {
	s     *RedisService // A reference back to the main service
	key   string
	value string
	ttl   time.Duration
}

// NewLock is a new method on your RedisService that acts as a factory.
func (s *RedisService) NewLock(key string, ttl time.Duration) *Lock {
	return &Lock{
		s:     s,
		key:   key,
		value: uuid.New().String(), // Each lock instance gets a unique value.
		ttl:   ttl,
	}
}

// TryLock attempts to acquire the lock. Returns true if successful.
func (l *Lock) TryLock(ctx context.Context) (bool, error) {
	ok, err := l.s.rc.SetNX(ctx, l.key, l.value, l.ttl).Result()
	if err != nil {
		return false, fmt.Errorf("redis SetNX error for key %s: %w", l.key, err)
	}
	return ok, nil
}

// Unlock releases the lock.
func (l *Lock) Unlock(ctx context.Context) error {
	_, err := l.s.unlockScriptExec.Eval(ctx, l.s.rc, []string{l.key}, l.value).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("redis Eval error for unlock script on key %s: %w", l.key, err)
	}
	return nil
}

// Refresh extends the TTL of the lock.
func (l *Lock) Refresh(ctx context.Context) error {
	ttlSeconds := int(l.ttl.Seconds())
	renewed, err := l.s.renewScriptExec.Eval(ctx, l.s.rc, []string{l.key}, l.value, ttlSeconds).Int64()
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("redis Eval error for renew script on key %s: %w", l.key, err)
	}

	if renewed == 0 {
		return errors.New("lock expired or was taken by another process")
	}

	return nil
}

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

// AcquireJanitorLeaderLock attempts to acquire a distributed lock for a janitor leader election.
func (s *RedisService) AcquireJanitorLeaderLock(ctx context.Context, ttl time.Duration) (acquired bool, lockValue string, err error) {
	val := uuid.New().String()

	ok, err := s.rc.SetNX(ctx, janitorLockKey, val, ttl).Result()
	if err != nil {
		return false, "", fmt.Errorf("redis SetNX error for key %s: %w", janitorLockKey, err)
	}

	if !ok {
		return false, "", nil
	}

	return true, val, nil
}

// ReleaseJanitorLeadershipLock safely releases a janitor task lock.
func (s *RedisService) ReleaseJanitorLeadershipLock(ctx context.Context, lockValue string, log *logrus.Entry) {
	if lockValue == "" {
		return
	}
	_, err := s.unlockScriptExec.Eval(ctx, s.rc, []string{janitorLockKey}, lockValue).Result()
	if err != nil {
		log.WithError(err).Errorf("failed to unlock janitor leadership with lockValue %s", lockValue)
	}
}

// RenewJanitorLeadershipLock extends the TTL of a lock if it's still held by the same owner.
// Returns true if the lock was successfully renewed.
func (s *RedisService) RenewJanitorLeadershipLock(ctx context.Context, lockValue string, ttl time.Duration) (bool, error) {
	ttlSeconds := int(ttl.Seconds())
	if ttlSeconds < 1 {
		return false, errors.New("TTL must be at least 1 second")
	}

	renewed, err := s.renewScriptExec.Eval(ctx, s.rc, []string{janitorLockKey}, lockValue, ttlSeconds).Int64()
	if errors.Is(err, redis.Nil) {
		// Key doesn't exist, so we couldn't renew it.
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("redis Eval error for renew script on key %s: %w", janitorLockKey, err)
	}

	return renewed == 1, nil
}
