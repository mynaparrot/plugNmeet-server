package models

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

const (
	// Default for how long CreateRoom will try to acquire a lock
	defaultRoomCreationMaxWaitTime = 15 * time.Second
	// Default TTL for the Redis lock key itself during creation
	defaultRoomCreationLockTTL = 60 * time.Second

	// Default for how long other operations (EndRoom, GetInfo) wait for creation to complete
	defaultWaitForRoomCreationMaxWaitTime = 15 * time.Second

	// Exponential backoff settings
	backoffInitialInterval = 100 * time.Millisecond
	backoffMaxInterval     = 2 * time.Second
	backoffMultiplier      = 2.0
	backoffJitter          = 0.2
)

func acquireRoomCreationLockWithRetry(ctx context.Context, rs *redisservice.RedisService, roomID string, log *logrus.Entry) (string, error) {
	maxWaitTime := defaultRoomCreationMaxWaitTime
	lockTTL := defaultRoomCreationLockTTL
	currentInterval := backoffInitialInterval

	loopStartTime := time.Now()
	log.Info("attempting to acquire room creation lock")

	for {
		select {
		case <-ctx.Done():
			log.WithError(ctx.Err()).Warn("Context cancelled while waiting for room creation lock")
			return "", fmt.Errorf("lock acquisition cancelled for room '%s': %w", roomID, ctx.Err())
		default:
		}

		acquired, lockValue, errLock := rs.LockRoomCreation(ctx, roomID, lockTTL)
		if errLock != nil {
			log.WithError(errLock).Error("Redis error while attempting to acquire room creation lock")
			return "", fmt.Errorf("redis communication error for room '%s' lock: %w", roomID, errLock)
		}

		if acquired {
			log.WithFields(logrus.Fields{
				"lockValue": lockValue,
				"duration":  time.Since(loopStartTime),
			}).Info("successfully acquired room creation lock")
			return lockValue, nil
		}

		if time.Since(loopStartTime) >= maxWaitTime {
			log.WithField("maxWaitTime", maxWaitTime).Warn("Timeout while waiting for room creation lock")
			return "", errors.New("timeout waiting to acquire lock for room " + roomID + ", operation is currently locked")
		}

		// Calculate next interval with jitter
		jitter := time.Duration(rand.Float64() * backoffJitter * float64(currentInterval))
		waitDuration := currentInterval + jitter

		log.WithFields(logrus.Fields{
			"waitDuration": waitDuration,
			"elapsed":      time.Since(loopStartTime),
		}).Debug("Room creation lock not acquired. Waiting.")
		select {
		case <-time.After(waitDuration):
		case <-ctx.Done():
			log.WithError(ctx.Err()).Warn("Context cancelled while polling for room creation lock")
			return "", fmt.Errorf("lock acquisition polling cancelled for room '%s': %w", roomID, ctx.Err())
		}
		currentInterval = time.Duration(float64(currentInterval) * backoffMultiplier)
		if currentInterval > backoffMaxInterval {
			currentInterval = backoffMaxInterval
		}
	}
}

// waitUntilRoomCreationCompletes waits until the room creation lock for the given roomID is released.
func waitUntilRoomCreationCompletes(ctx context.Context, rs *redisservice.RedisService, roomID string, log *logrus.Entry) error {
	maxWaitTime := defaultWaitForRoomCreationMaxWaitTime
	currentInterval := backoffInitialInterval
	loopStartTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			log.WithError(ctx.Err()).Warn("Context cancelled while waiting for room creation to complete")
			return fmt.Errorf("waiting for room creation to complete cancelled for room '%s': %w", roomID, ctx.Err())
		default:
		}

		isLocked, errCheck := rs.IsRoomCreationLock(ctx, roomID)
		if errCheck != nil {
			log.WithError(errCheck).Error("Redis error while checking room creation lock")
			return fmt.Errorf("redis communication error while checking room '%s' creation lock: %w", roomID, errCheck)
		}

		if !isLocked {
			return nil
		}

		if time.Since(loopStartTime) >= maxWaitTime {
			log.WithField("maxWaitTime", maxWaitTime).Warn("Timeout while waiting for room creation to complete")
			return fmt.Errorf("timeout waiting for room creation of room '%s' to complete", roomID)
		}

		// Calculate next interval with jitter
		jitter := time.Duration(rand.Float64() * backoffJitter * float64(currentInterval))
		waitDuration := currentInterval + jitter

		log.WithFields(logrus.Fields{
			"waitDuration": waitDuration,
			"elapsed":      time.Since(loopStartTime),
		}).Debug("Room creation is still in progress. Waiting.")
		select {
		case <-time.After(waitDuration):
		case <-ctx.Done():
			log.WithError(ctx.Err()).Warn("Context cancelled while polling for room creation to complete")
			return fmt.Errorf("polling for room creation to complete cancelled for room '%s': %w", roomID, ctx.Err())
		}
		currentInterval = time.Duration(float64(currentInterval) * backoffMultiplier)
		if currentInterval > backoffMaxInterval {
			currentInterval = backoffMaxInterval
		}
	}
}
