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

func NewPollModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, natsService *natsservice.NatsService, analyticsModel *AnalyticsModel, logger *logrus.Logger) *PollModel {
	return &PollModel{
		app:            app,
		ds:             ds,
		rs:             rs,
		natsService:    natsService,
		analyticsModel: analyticsModel,
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
