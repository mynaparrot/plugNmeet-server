package models

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"time"
)

type SchedulerModel struct {
	app         *config.AppConfig
	ds          *dbservice.DatabaseService
	rs          *redisservice.RedisService
	lk          *livekitservice.LivekitService
	natsService *natsservice.NatsService
	rm          *RoomModel

	rmDuration  *RoomDurationModel
	closeTicker chan bool
}

func NewSchedulerModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, lk *livekitservice.LivekitService) *SchedulerModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.New(app.DB)
	}
	if rs == nil {
		rs = redisservice.New(app.RDS)
	}
	if lk == nil {
		lk = livekitservice.New(app, rs)
	}

	return &SchedulerModel{
		app:         app,
		ds:          ds,
		rs:          rs,
		lk:          lk,
		rm:          NewRoomModel(app, ds, rs, lk),
		rmDuration:  NewRoomDurationModel(app, rs),
		natsService: natsservice.New(app),
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
		}
	}
}
