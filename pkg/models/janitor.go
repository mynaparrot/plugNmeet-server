package models

import (
	"context"
	"time"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

// JanitorModel performs various background cleanup and maintenance tasks for the application.
type JanitorModel struct {
	app         *config.AppConfig
	ds          *dbservice.DatabaseService
	rs          *redisservice.RedisService
	natsService *natsservice.NatsService
	rm          *RoomModel

	rmDuration *RoomDurationModel
	logger     *logrus.Entry
}

// NewJanitorModel creates a new JanitorModel.
func NewJanitorModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, natsService *natsservice.NatsService, rm *RoomModel, rmDuration *RoomDurationModel, logger *logrus.Logger) *JanitorModel {
	return &JanitorModel{
		app:         app,
		ds:          ds,
		rs:          rs,
		rm:          rm,
		rmDuration:  rmDuration,
		natsService: natsService,
		logger:      logger.WithField("model", "janitor"),
	}
}

// StartJanitor starts the background janitor process.
func (m *JanitorModel) StartJanitor(ctx context.Context) {
	// Base ticker runs at the highest frequency needed.
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Set initial schedules for less frequent tasks.
	nextUserCheck := time.Now().Add(time.Minute)
	nextRoomCheck := time.Now().Add(5 * time.Minute)
	nextBackupCheck := time.Now().Add(time.Hour)

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			// This task runs every 5 seconds.
			m.checkRoomWithDuration()

			if now.After(nextUserCheck) {
				m.checkOnlineUsersStatus()
				nextUserCheck = now.Add(time.Minute)
			}
			if now.After(nextRoomCheck) {
				m.activeRoomChecker()
				nextRoomCheck = now.Add(5 * time.Minute)
			}
			if now.After(nextBackupCheck) {
				m.checkDelRecordingBackupPath()
				nextBackupCheck = now.Add(time.Hour)
			}
		}
	}
}
