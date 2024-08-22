package roommodel

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	usermodel "github.com/mynaparrot/plugnmeet-server/pkg/models/user"
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
	userModel   *usermodel.UserModel
	natsService *natsservice.NatsService
}

func New(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, lk *livekitservice.LivekitService) *RoomModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.New(app.ORM)
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
		userModel:   usermodel.New(app, ds, rs, lk),
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
	// completely remove a room active users list
	_, err := m.rs.ManageActiveUsersList(roomId, "", "delList", 0)
	if err != nil {
		log.Errorln(err)
	}

	// delete blocked users list
	_, err = m.rs.DeleteRoomBlockList(roomId)
	if err != nil {
		log.Errorln(err)
	}

	// completely remove the room key
	_, err = m.rs.ManageRoomWithUsersMetadata(roomId, "", "delList", "")
	if err != nil {
		log.Errorln(err)
	}

	// remove this room from an active room list
	_, err = m.rs.ManageActiveRoomsWithMetadata(roomId, "del", "")
	if err != nil {
		log.Errorln(err)
	}

	// remove from progress, if existed. no need to log if error
	_, _ = m.rs.RoomCreationProgressList(roomId, "del")
}
