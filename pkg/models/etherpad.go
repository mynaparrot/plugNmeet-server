package models

import (
	"context"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

const (
	APIVersion = "1.3.0"
)

type EtherpadHttpRes struct {
	Code    int64             `json:"code"`
	Message string            `json:"message"`
	Data    EtherpadDataTypes `json:"data"`
}

type EtherpadDataTypes struct {
	AuthorID        string `json:"authorID"`
	GroupID         string `json:"groupID"`
	SessionID       string `json:"sessionID"`
	PadID           string `json:"padID"`
	ReadOnlyID      string `json:"readOnlyID"`
	TotalPads       int64  `json:"totalPads"`
	TotalSessions   int64  `json:"totalSessions"`
	TotalActivePads int64  `json:"totalActivePads"`
}

type EtherpadModel struct {
	ctx            context.Context
	app            *config.AppConfig
	ds             *dbservice.DatabaseService
	rs             *redisservice.RedisService
	lk             *livekitservice.LivekitService
	analyticsModel *AnalyticsModel
	natsService    *natsservice.NatsService
	logger         *logrus.Entry
}

type EtherpadModelArgs struct {
	fx.In
	Ctx            context.Context
	App            *config.AppConfig
	Ds             *dbservice.DatabaseService
	Rs             *redisservice.RedisService
	NatsService    *natsservice.NatsService
	AnalyticsModel *AnalyticsModel
	Logger         *logrus.Logger
}

func NewEtherpadModel(args EtherpadModelArgs) *EtherpadModel {
	return &EtherpadModel{
		ctx:            args.Ctx,
		app:            args.App,
		ds:             args.Ds,
		rs:             args.Rs,
		analyticsModel: args.AnalyticsModel,
		natsService:    args.NatsService,
		logger:         args.Logger.WithField("model", "etherpad"),
	}
}
