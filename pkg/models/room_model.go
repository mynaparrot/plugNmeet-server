package models

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	log "github.com/sirupsen/logrus"
	"time"
)

type RoomModel struct {
	app         *config.AppConfig
	ds          *dbservice.DatabaseService
	rs          *redisservice.RedisService
	lk          *livekitservice.LivekitService
	userModel   *UserModel
	natsService *natsservice.NatsService
}

func NewRoomModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, lk *livekitservice.LivekitService) *RoomModel {
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

	return &RoomModel{
		app:         app,
		ds:          ds,
		rs:          rs,
		lk:          lk,
		userModel:   NewUserModel(app, ds, rs, lk),
		natsService: natsservice.New(app),
	}
}

// CheckAndWaitUntilRoomCreationInProgress will check the process & wait if needed
func (m *RoomModel) CheckAndWaitUntilRoomCreationInProgress(roomId string) {
	for {
		list, err := m.rs.RoomCreationProgressList(roomId, "exist")
		if err != nil {
			log.Errorln(err)
			break
		}
		if list {
			log.Println(roomId, "creation in progress, so waiting for", config.WaitDurationIfRoomInProgress)
			// we'll wait
			time.Sleep(config.WaitDurationIfRoomInProgress)
		} else {
			break
		}
	}
}

func (m *RoomModel) OnAfterRoomClosed(roomId string) {
	// delete blocked users list
	_, err := m.rs.DeleteRoomBlockList(roomId)
	if err != nil {
		log.Errorln(err)
	}

	// remove from progress, if existed. no need to log if error
	_, _ = m.rs.RoomCreationProgressList(roomId, "del")
}
