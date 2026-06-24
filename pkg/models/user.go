package models

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

type UserModel struct {
	app            *config.AppConfig
	ds             *dbservice.DatabaseService
	rs             *redisservice.RedisService
	lk             *livekitservice.LivekitService
	natsService    *natsservice.NatsService
	analyticsModel *AnalyticsModel
	am             *AuthModel
	logger         *logrus.Entry
}

type UserModelArgs struct {
	fx.In
	App            *config.AppConfig
	Ds             *dbservice.DatabaseService
	Rs             *redisservice.RedisService
	Lk             *livekitservice.LivekitService
	NatsService    *natsservice.NatsService
	AnalyticsModel *AnalyticsModel
	Am             *AuthModel
	Logger         *logrus.Logger
}

func NewUserModel(args UserModelArgs) *UserModel {
	return &UserModel{
		app:            args.App,
		ds:             args.Ds,
		rs:             args.Rs,
		lk:             args.Lk,
		natsService:    args.NatsService,
		analyticsModel: args.AnalyticsModel,
		am:             args.Am,
		logger:         args.Logger.WithField("model", "user"),
	}
}
