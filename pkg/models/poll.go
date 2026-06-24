package models

import (
	"errors"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

type PollModel struct {
	app            *config.AppConfig
	ds             *dbservice.DatabaseService
	rs             *redisservice.RedisService
	analyticsModel *AnalyticsModel
	natsService    *natsservice.NatsService
	logger         *logrus.Entry
}

type PollModelArgs struct {
	fx.In
	App            *config.AppConfig
	Ds             *dbservice.DatabaseService
	Rs             *redisservice.RedisService
	NatsService    *natsservice.NatsService
	AnalyticsModel *AnalyticsModel
	Logger         *logrus.Logger
}

func NewPollModel(args PollModelArgs) *PollModel {
	return &PollModel{
		app:            args.App,
		ds:             args.Ds,
		rs:             args.Rs,
		natsService:    args.NatsService,
		analyticsModel: args.AnalyticsModel,
		logger:         args.Logger.WithField("model", "poll"),
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
