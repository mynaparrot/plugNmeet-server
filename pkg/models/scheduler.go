package models

import (
	"time"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

type SchedulerModel struct {
	app         *config.AppConfig
	ds          *dbservice.DatabaseService
	rs          *redisservice.RedisService
	natsService *natsservice.NatsService
	rm          *RoomModel

	rmDuration  *RoomDurationModel
	closeTicker chan bool
	logger      *logrus.Entry
}

func NewSchedulerModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, logger *logrus.Logger) *SchedulerModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.New(app.DB, logger)
	}
	if rs == nil {
		rs = redisservice.New(app.RDS, logger)
	}

	return &SchedulerModel{
		app:         app,
		ds:          ds,
		rs:          rs,
		rm:          NewRoomModel(app, ds, rs, logger),
		rmDuration:  NewRoomDurationModel(app, rs, logger),
		natsService: natsservice.New(app, logger),
		logger:      logger.WithField("model", "scheduler"),
	}
}

func (m *SchedulerModel) StartScheduler() {
	m.closeTicker = make(chan bool)
	checkRoomDuration := time.NewTicker(5 * time.Second)
	defer checkRoomDuration.Stop()

	fiveMinutesChecker := time.NewTicker(5 * time.Minute)
	defer fiveMinutesChecker.Stop()

	oneMinuteChecker := time.NewTicker(1 * time.Minute)
	defer oneMinuteChecker.Stop()

	hourlyChecker := time.NewTicker(1 * time.Hour)
	defer hourlyChecker.Stop()

	for {
		select {
		case <-m.closeTicker:
			return
		case <-checkRoomDuration.C:
			m.checkRoomWithDuration()
		case <-oneMinuteChecker.C:
			m.checkOnlineUsersStatus()
		case <-fiveMinutesChecker.C:
			m.activeRoomChecker()
		case <-hourlyChecker.C:
			m.checkDelRecordingBackupPath()
		}
	}
}
