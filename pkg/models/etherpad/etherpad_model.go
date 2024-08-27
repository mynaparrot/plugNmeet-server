package etherpadmodel

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/analytics"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
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
	NodeId       string
	Host         string
	ClientId     string
	ClientSecret string

	app            *config.AppConfig
	ds             *dbservice.DatabaseService
	rs             *redisservice.RedisService
	lk             *livekitservice.LivekitService
	analyticsModel *analyticsmodel.AnalyticsModel
	natsService    *natsservice.NatsService
}

func New(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService) *EtherpadModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.New(app.DB)
	}
	if rs == nil {
		rs = redisservice.New(app.RDS)
	}

	return &EtherpadModel{
		app:            app,
		ds:             ds,
		rs:             rs,
		analyticsModel: analyticsmodel.New(app, ds, rs),
		natsService:    natsservice.New(app),
	}
}
