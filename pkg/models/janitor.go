package models

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	dbservice "github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	livekitservice "github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

// JanitorModel performs various background cleanup and maintenance tasks for the application.
type JanitorModel struct {
	mu          sync.RWMutex
	ctx         context.Context
	cancel      context.CancelFunc
	app         *config.AppConfig
	ds          *dbservice.DatabaseService
	rs          *redisservice.RedisService
	natsService *natsservice.NatsService
	lk          *livekitservice.LivekitService
	rm          *RoomModel

	rmDuration    *RoomDurationModel
	artifactModel *ArtifactModel
	logger        *logrus.Entry

	// leader election for janitor
	leaderLockVal string
	leaderLockTTL time.Duration
	leaderRenewal time.Duration
}

// NewJanitorModel creates a new JanitorModel.
func NewJanitorModel(mainCtx context.Context, app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, natsService *natsservice.NatsService, lk *livekitservice.LivekitService, rm *RoomModel, rmDuration *RoomDurationModel, artifactModel *ArtifactModel, logger *logrus.Logger) *JanitorModel {
	ctx, cancel := context.WithCancel(mainCtx)

	return &JanitorModel{
		ctx:           ctx,
		cancel:        cancel,
		app:           app,
		ds:            ds,
		rs:            rs,
		lk:            lk,
		rm:            rm,
		artifactModel: artifactModel,
		rmDuration:    rmDuration,
		natsService:   natsService,
		logger:        logger.WithField("model", "janitor"),

		leaderLockTTL: 1 * time.Minute,
		leaderRenewal: 30 * time.Second,
	}
}

// StartJanitor starts the background janitor process.
// It uses a leader election mechanism to ensure only one instance runs the tasks.
func (m *JanitorModel) StartJanitor() {
	m.logger.Infoln("Janitor starting, attempting to acquire leader lock...")

	for {
		select {
		case <-m.ctx.Done():
			m.logger.WithError(m.ctx.Err()).Infoln("Janitor shutdown completed")
			return
		default:
			acquired, lockVal, err := m.rs.AcquireJanitorLeaderLock(m.ctx, m.leaderLockTTL)
			if err != nil {
				if !errors.Is(err, redis.Nil) {
					m.logger.WithError(err).Errorln("Failed to check for janitor leader lock")
				}
				// Wait before retrying to avoid spamming Redis on error
				time.Sleep(m.leaderRenewal)
				continue
			}

			if acquired {
				m.logger.WithField("lockVal", lockVal).Infoln("Acquired janitor leader lock. Starting tasks.")
				m.mu.Lock()
				m.leaderLockVal = lockVal
				m.mu.Unlock()
				// We are the leader. Run the tasks until we lose the lock or context is canceled.
				m.runJanitorTasks()
				m.logger.Warnln("Stopped being the janitor leader.")
			} else {
				// Not the leader, wait and try again later.
				time.Sleep(m.leaderRenewal)
			}
		}
	}
}

// runJanitorTasks contains the main loop for performing cleanup tasks.
// This is only executed by the instance that holds the leader lock.
func (m *JanitorModel) runJanitorTasks() {
	// Lock renewal ticker
	renewalTicker := time.NewTicker(m.leaderRenewal)
	defer renewalTicker.Stop()

	// Task ticker runs at the highest frequency needed.
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Set initial schedules for less frequent tasks.
	nextUserCheck := time.Now().Add(time.Minute)
	nextRoomCheck := time.Now().Add(5 * time.Minute)
	nextBackupCheck := time.Now().Add(time.Hour)
	nextSummarizeCheck := time.Now().Add(5 * time.Minute)

	for {
		select {
		case <-m.ctx.Done():
			// Context canceled
			return
		case now := <-ticker.C:
			// These tasks run on their own schedule.
			// The individual locks inside each task ensure safety if the leader changes mid-operation.
			m.checkRoomWithDuration()

			if now.After(nextUserCheck) {
				m.checkOnlineUsersStatus()
				nextUserCheck = time.Now().Add(time.Minute)
			}
			if now.After(nextRoomCheck) {
				m.activeRoomChecker()
				nextRoomCheck = time.Now().Add(5 * time.Minute)
			}
			if now.After(nextBackupCheck) {
				m.checkDelRecordingBackupPath()
				nextBackupCheck = time.Now().Add(time.Hour)
			}
			if now.After(nextSummarizeCheck) {
				m.CheckInsightsPendingSummarizeJobs()
				nextSummarizeCheck = time.Now().Add(5 * time.Minute)
			}
		case <-renewalTicker.C:
			// Copy the lock value to a local var to avoid holding the lock during a network call.
			m.mu.RLock()
			currentLockVal := m.leaderLockVal
			m.mu.RUnlock()

			// Renew the leader lock.
			renewed, err := m.rs.RenewJanitorLeadershipLock(m.ctx, currentLockVal, m.leaderLockTTL)
			if err != nil {
				m.logger.WithError(err).Errorln("Failed to renew janitor leader lock")
			}
			if !renewed {
				// We lost the lock. Stop being the leader and return to the election loop.
				return
			}
		}
	}
}

func (m *JanitorModel) Shutdown() {
	m.logger.Infoln("Janitor shutting down.")
	// Copy the lock value to a local var to avoid holding the lock during a network call.
	m.mu.RLock()
	currentLockVal := m.leaderLockVal
	m.mu.RUnlock()

	m.rs.ReleaseJanitorLeadershipLock(m.ctx, currentLockVal, m.logger)
	m.cancel()
}
