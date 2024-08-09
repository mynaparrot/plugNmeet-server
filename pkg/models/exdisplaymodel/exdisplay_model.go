package exdisplaymodel

import (
	"errors"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/dbservice"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekitservice"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redisservice"
)

type ExDisplayModel struct {
	app *config.AppConfig
	ds  *dbservice.DatabaseService
	rs  *redisservice.RedisService
	lk  *livekitservice.LivekitService
}

type updateRoomMetadataOpts struct {
	isActive *bool
	sharedBy *string
	url      *string
}

func New(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, lk *livekitservice.LivekitService) *ExDisplayModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.New(app.ORM)
	}
	if rs == nil {
		rs = redisservice.New(app.RDS)
	}
	if lk == nil {
		lk = livekitservice.New(app, rs)
	}

	return &ExDisplayModel{
		app: app,
		ds:  ds,
		rs:  rs,
		lk:  lk,
	}
}

func (m *ExDisplayModel) HandleTask(req *plugnmeet.ExternalDisplayLinkReq) error {
	switch req.Task {
	case plugnmeet.ExternalDisplayLinkTask_START_EXTERNAL_LINK:
		return m.start(req)
	case plugnmeet.ExternalDisplayLinkTask_STOP_EXTERNAL_LINK:
		return m.end(req)
	}

	return errors.New("not valid request")
}
