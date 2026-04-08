package models

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
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

var timeoutErr = errors.New("timeout reached")

type RoomModel struct {
	ctx             context.Context
	app             *config.AppConfig
	ds              *dbservice.DatabaseService
	rs              *redisservice.RedisService
	lk              *livekitservice.LivekitService
	natsService     *natsservice.NatsService
	webhookNotifier *helpers.WebhookNotifier
	logger          *logrus.Entry
	userModel       *UserModel
	recordingModel  *RecordingModel
	fileModel       *FileModel
	etherpadModel   *EtherpadModel
	pollModel       *PollModel
	analyticsModel  *AnalyticsModel
	breakoutModel   *BreakoutRoomModel
	insightsModel   *InsightsModel
}

type updateRoomMetadataOpts struct {
	isActive *bool
	sharedBy *string
	url      *string
}

func NewRoomModel(ctx context.Context, app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, lk *livekitservice.LivekitService, natsService *natsservice.NatsService, webhookNotifier *helpers.WebhookNotifier, userModel *UserModel, recordingModel *RecordingModel, fileModel *FileModel, etherpadModel *EtherpadModel, pollModel *PollModel, analyticsModel *AnalyticsModel, insightsModel *InsightsModel, logger *logrus.Logger) *RoomModel {
	return &RoomModel{
		ctx:             ctx,
		app:             app,
		ds:              ds,
		rs:              rs,
		lk:              lk,
		natsService:     natsService,
		webhookNotifier: webhookNotifier,
		userModel:       userModel,
		recordingModel:  recordingModel,
		fileModel:       fileModel,
		etherpadModel:   etherpadModel,
		pollModel:       pollModel,
		analyticsModel:  analyticsModel,
		insightsModel:   insightsModel,
		logger:          logger.WithField("model", "room"),
	}
}

// SetBreakoutRoomModel is an initializer to prevent circular dependency.
func (m *RoomModel) SetBreakoutRoomModel(bm *BreakoutRoomModel) {
	m.breakoutModel = bm
}

// performWithBackoff is a helper that executes a function with an exponential backoff retry strategy.
func performWithBackoff(ctx context.Context, maxWaitTime time.Duration, log *logrus.Entry, action func() (bool, error)) error {
	currentInterval := backoffInitialInterval
	loopStartTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			log.WithError(ctx.Err()).Warn("Context cancelled during backoff")
			return ctx.Err()
		default:
		}

		done, err := action()
		if err != nil {
			log.WithError(err).Error("Error during backoff action")
			return err
		}

		if done {
			return nil
		}

		if time.Since(loopStartTime) >= maxWaitTime {
			log.WithField("maxWaitTime", maxWaitTime).Warn("Timeout during backoff")
			return timeoutErr
		}

		// Calculate next interval with jitter
		jitter := time.Duration(rand.Float64() * backoffJitter * float64(currentInterval))
		waitDuration := currentInterval + jitter

		log.WithFields(logrus.Fields{
			"waitDuration": waitDuration,
			"elapsed":      time.Since(loopStartTime),
		}).Debug("Action not complete. Waiting.")
		select {
		case <-time.After(waitDuration):
		case <-ctx.Done():
			log.WithError(ctx.Err()).Warn("Context cancelled while waiting for next backoff attempt")
			return ctx.Err()
		}
		currentInterval = time.Duration(float64(currentInterval) * backoffMultiplier)
		if currentInterval > backoffMaxInterval {
			currentInterval = backoffMaxInterval
		}
	}
}

func acquireRoomCreationLockWithRetry(ctx context.Context, rs *redisservice.RedisService, roomID string, log *logrus.Entry) (string, error) {
	maxWaitTime := defaultRoomCreationMaxWaitTime
	lockTTL := defaultRoomCreationLockTTL
	var lockValue string

	log.Info("Attempting to acquire room creation lock")

	action := func() (bool, error) {
		acquired, val, err := rs.LockRoomCreation(ctx, roomID, lockTTL)
		if err != nil {
			return false, fmt.Errorf("redis communication error for room '%s' lock: %w", roomID, err)
		}
		if acquired {
			lockValue = val
			return true, nil
		}
		return false, nil
	}

	err := performWithBackoff(ctx, maxWaitTime, log, action)
	if err != nil {
		if errors.Is(err, timeoutErr) {
			return "", errors.New("timeout waiting to acquire lock for room " + roomID + ", operation is currently locked")
		}
		return "", fmt.Errorf("lock acquisition cancelled for room '%s': %w", roomID, err)
	}

	log.WithFields(logrus.Fields{
		"lockValue": lockValue,
	}).Info("Successfully acquired room creation lock")
	return lockValue, nil
}

// waitUntilRoomCreationCompletes waits until the room creation lock for the given roomID is released.
func waitUntilRoomCreationCompletes(ctx context.Context, rs *redisservice.RedisService, roomID string, log *logrus.Entry) error {
	maxWaitTime := defaultWaitForRoomCreationMaxWaitTime

	action := func() (bool, error) {
		isLocked, err := rs.IsRoomCreationLock(ctx, roomID)
		if err != nil {
			return false, fmt.Errorf("redis communication error while checking room '%s' creation lock: %w", roomID, err)
		}
		return !isLocked, nil
	}

	err := performWithBackoff(ctx, maxWaitTime, log, action)
	if err != nil {
		if errors.Is(err, timeoutErr) {
			return fmt.Errorf("timeout waiting for room creation of room '%s' to complete", roomID)
		}
		return fmt.Errorf("waiting for room creation to complete cancelled for room '%s': %w", roomID, err)
	}

	return nil
}
