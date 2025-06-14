package models

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"time"
)

const (
	// Default for how long CreateRoom will try to acquire a lock
	defaultRoomCreationMaxWaitTime = 30 * time.Second
	// Default for how often CreateRoom polls for the lock
	defaultRoomCreationLockPollInterval = 250 * time.Millisecond
	// Default TTL for the Redis lock key itself during creation
	defaultRoomCreationLockTTL = 60 * time.Second

	// Default for how long other operations (EndRoom, GetInfo) wait for creation to complete
	defaultWaitForRoomCreationMaxWaitTime = 30 * time.Second
	// Default for how often other operations poll to see if creation lock is released
	defaultWaitForRoomCreationPollInterval = 250 * time.Millisecond
)

type RoomModel struct {
	app         *config.AppConfig
	ds          *dbservice.DatabaseService
	rs          *redisservice.RedisService
	lk          *livekitservice.LivekitService
	userModel   *UserModel
	natsService *natsservice.NatsService
}

func NewRoomModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService) *RoomModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.New(app.DB)
	}
	if rs == nil {
		rs = redisservice.New(app.RDS)
	}

	return &RoomModel{
		app:         app,
		ds:          ds,
		rs:          rs,
		lk:          livekitservice.New(app),
		userModel:   NewUserModel(app, ds, rs),
		natsService: natsservice.New(app),
	}
}
