package websocketmodel

import (
	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/dbservice"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekitservice"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redisservice"
	"time"
)

type WebsocketModel struct {
	app *config.AppConfig
	ds  *dbservice.DatabaseService
	rs  *redisservice.RedisService
	lk  *livekitservice.LivekitService
}

func New(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, lk *livekitservice.LivekitService) *WebsocketModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.NewDBService(app.ORM)
	}
	if rs == nil {
		rs = redisservice.NewRedisService(app.RDS)
	}
	if lk == nil {
		lk = livekitservice.NewLivekitService(app, rs)
	}

	return &WebsocketModel{
		app: app,
		ds:  ds,
		rs:  rs,
		lk:  lk,
	}
}

func (m *WebsocketModel) HandleDataMessages(payload *plugnmeet.DataMessage, roomId string) {
	if payload.MessageId == nil {
		uu := uuid.NewString()
		payload.MessageId = &uu
	}
	if payload.Body.Time == nil {
		tt := time.Now().UTC().Format(time.RFC1123Z)
		payload.Body.Time = &tt
	}

	if payload.To != nil && len(*payload.To) == 0 {
		payload.To = nil
	}

	switch payload.Type {
	case plugnmeet.DataMsgType_USER:
		m.userMessages(payload, roomId)
	case plugnmeet.DataMsgType_SYSTEM:
		m.handleSystemMessages(payload, roomId)
	case plugnmeet.DataMsgType_WHITEBOARD:
		m.handleWhiteboardMessages(payload, roomId)
	}
}
