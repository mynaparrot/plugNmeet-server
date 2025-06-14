package models

import (
	"context"
	"errors"
	"fmt"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	log "github.com/sirupsen/logrus"
	"time"
)

func acquireRoomCreationLockWithRetry(ctx context.Context, rs *redisservice.RedisService, roomID string) (string, error) {
	maxWaitTime := defaultRoomCreationMaxWaitTime
	pollInterval := defaultRoomCreationLockPollInterval
	lockTTL := defaultRoomCreationLockTTL

	loopStartTime := time.Now()
	log.Infof("attempting to acquire creation lock for room: '%s'", roomID)

	for {
		select {
		case <-ctx.Done():
			log.Warnf("Context cancelled while waiting for room creation lock for '%s': %v", roomID, ctx.Err())
			return "", fmt.Errorf("lock acquisition cancelled for room '%s': %w", roomID, ctx.Err())
		default:
		}

		acquired, lockValue, errLock := rs.LockRoomCreation(ctx, roomID, lockTTL)
		if errLock != nil {
			log.Errorf("Redis error  while attempting to acquire room creation lock for '%s': %v", roomID, errLock)
			return "", fmt.Errorf("redis communication error for room '%s' lock: %w", roomID, errLock)
		}

		if acquired {
			log.Infof("successfully acquired room creation lock for '%s' (lockValue: %s) after %v", roomID, lockValue, time.Since(loopStartTime))
			return lockValue, nil
		}

		if time.Since(loopStartTime) >= maxWaitTime {
			log.Warnf("Timeout while waiting %v for room creation lock for '%s'.", maxWaitTime, roomID)
			return "", errors.New("timeout waiting to acquire lock for room " + roomID + ", operation is currently locked")
		}

		log.Debugf("Room creation lock not acquired for roomId: %s. Waiting %v. Elapsed: %v", roomID, pollInterval, time.Since(loopStartTime))
		select {
		case <-time.After(pollInterval):
		case <-ctx.Done():
			log.Warnf("Context cancelled while polling for room creation lock for '%s': %v", roomID, ctx.Err())
			return "", fmt.Errorf("lock acquisition polling cancelled for room '%s': %w", roomID, ctx.Err())
		}
	}
}

// waitUntilRoomCreationCompletes waits until the room creation lock for the given roomID is released.
func waitUntilRoomCreationCompletes(ctx context.Context, rs *redisservice.RedisService, roomID string) error {
	maxWaitTime := defaultWaitForRoomCreationMaxWaitTime
	pollInterval := defaultWaitForRoomCreationPollInterval

	loopStartTime := time.Now()
	log.Infof("checking if room creation is in progress for room: '%s' and will wait if so.", roomID)

	for {
		select {
		case <-ctx.Done():
			log.Warnf("Context cancelled while waiting for room creation to complete for room '%s': %v", roomID, ctx.Err())
			return fmt.Errorf("waiting for room creation to complete cancelled for room '%s': %w", roomID, ctx.Err())
		default:
		}

		isLocked, errCheck := rs.IsRoomCreationLock(ctx, roomID)
		if errCheck != nil {
			log.Errorf("Redis error while checking room creation lock for room '%s': %v", roomID, errCheck)
			return fmt.Errorf("redis communication error while checking room '%s' creation lock: %w", roomID, errCheck)
		}

		if !isLocked {
			log.Infof("Room creation for room '%s' is not in progress. Can proceed after %v of waiting.", roomID, time.Since(loopStartTime))
			return nil
		}

		if time.Since(loopStartTime) >= maxWaitTime {
			log.Warnf("Timeout while waiting %v for room creation to complete for room '%s'.", maxWaitTime, roomID)
			return fmt.Errorf("timeout waiting for room creation of room '%s' to complete", roomID)
		}

		log.Debugf("Room creation for %s is still in progress. Waiting %v. Elapsed: %v", roomID, pollInterval, time.Since(loopStartTime))
		select {
		case <-time.After(pollInterval):
		case <-ctx.Done():
			log.Warnf("Context cancelled while polling for room creation to complete for room '%s': %v", roomID, ctx.Err())
			return fmt.Errorf("polling for room creation to complete cancelled for room '%s': %w", roomID, ctx.Err())
		}
	}
}
