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
		locked := m.rs.IsRoomCreationLock(roomId)
		if locked {
			log.Println(roomId, "room creation locked, waiting for:", config.WaitDurationIfRoomCreationLocked)
			time.Sleep(config.WaitDurationIfRoomCreationLocked)
		} else {
			break
		}
	}
}
