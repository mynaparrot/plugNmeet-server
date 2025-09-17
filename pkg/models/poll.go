package models

import (
	"errors"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

type PollModel struct {
	app            *config.AppConfig
	ds             *dbservice.DatabaseService
	rs             *redisservice.RedisService
	analyticsModel *AnalyticsModel
	natsService    *natsservice.NatsService
	logger         *logrus.Entry
}

func NewPollModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, logger *logrus.Logger) *PollModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.New(app.DB, logger)
	}
	if rs == nil {
		rs = redisservice.New(app.RDS, logger)
	}

	return &PollModel{
		app:            app,
		ds:             ds,
		rs:             rs,
		analyticsModel: NewAnalyticsModel(app, ds, rs, logger),
		natsService:    natsservice.New(app, logger),
		logger:         logger.WithField("model", "poll"),
	}
}

func (m *PollModel) ManageActivation(req *plugnmeet.ActivatePollsReq) error {
	roomMeta, err := m.natsService.GetRoomMetadataStruct(req.GetRoomId())
	if err != nil {
		return err
	}
	if roomMeta == nil {
		return errors.New("invalid nil room metadata information")
	}

	roomMeta.RoomFeatures.PollsFeatures.IsActive = req.GetIsActive()
	return m.natsService.UpdateAndBroadcastRoomMetadata(req.GetRoomId(), roomMeta)
}
