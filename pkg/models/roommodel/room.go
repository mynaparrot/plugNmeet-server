package roommodel

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/dbservice"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekitservice"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redisservice"
	log "github.com/sirupsen/logrus"
	"time"
)

type RoomModel struct {
	app *config.AppConfig
	ds  *dbservice.DatabaseService
	rs  *redisservice.RedisService
	lk  *livekitservice.LivekitService
}

func NewRoomModel() *RoomModel {
	app := config.GetConfig()
	ds := dbservice.NewDBService(app.ORM)
	rs := redisservice.NewRedisService(app.RDS)
	lk := livekitservice.NewLivekitService(rs)

	return &RoomModel{
		app: app,
		ds:  ds,
		rs:  rs,
		lk:  lk,
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
