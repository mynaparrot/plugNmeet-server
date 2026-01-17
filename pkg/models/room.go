package models

import (
	"context"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

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
	recorderModel   *RecorderModel
	fileModel       *FileModel
	roomDuration    *RoomDurationModel
	etherpadModel   *EtherpadModel
	pollModel       *PollModel
	analyticsModel  *AnalyticsModel
	breakoutModel   *BreakoutRoomModel
	insightsModel   *InsightsModel
}

func NewRoomModel(ctx context.Context, app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, lk *livekitservice.LivekitService, natsService *natsservice.NatsService, webhookNotifier *helpers.WebhookNotifier, userModel *UserModel, recorderModel *RecorderModel, fileModel *FileModel, roomDuration *RoomDurationModel, etherpadModel *EtherpadModel, pollModel *PollModel, analyticsModel *AnalyticsModel, insightsModel *InsightsModel, logger *logrus.Logger) *RoomModel {
	return &RoomModel{
		ctx:             ctx,
		app:             app,
		ds:              ds,
		rs:              rs,
		lk:              lk,
		natsService:     natsService,
		webhookNotifier: webhookNotifier,
		userModel:       userModel,
		recorderModel:   recorderModel,
		fileModel:       fileModel,
		roomDuration:    roomDuration,
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
