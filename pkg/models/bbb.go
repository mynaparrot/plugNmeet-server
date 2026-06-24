package models

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

type BBBApiWrapperModel struct {
	app    *config.AppConfig
	ds     *dbservice.DatabaseService
	rs     *redisservice.RedisService
	rrm    *RecordingModel
	logger *logrus.Entry
}

type BBBApiWrapperModelArgs struct {
	fx.In
	App    *config.AppConfig
	Ds     *dbservice.DatabaseService
	Rs     *redisservice.RedisService
	Rrm    *RecordingModel
	Logger *logrus.Logger
}

func NewBBBApiWrapperModel(args BBBApiWrapperModelArgs) *BBBApiWrapperModel {
	return &BBBApiWrapperModel{
		app:    args.App,
		ds:     args.Ds,
		rs:     args.Rs,
		rrm:    args.Rrm,
		logger: args.Logger.WithField("model", "bbb"),
	}
}
