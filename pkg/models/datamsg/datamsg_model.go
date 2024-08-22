package datamsgmodel

import (
	"errors"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/analytics"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
)

type DataMsgModel struct {
	app            *config.AppConfig
	ds             *dbservice.DatabaseService
	rs             *redisservice.RedisService
	lk             *livekitservice.LivekitService
	analyticsModel *analyticsmodel.AnalyticsModel
	natsService    *natsservice.NatsService
}

func New(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, lk *livekitservice.LivekitService) *DataMsgModel {
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

	return &DataMsgModel{
		app:            app,
		ds:             ds,
		rs:             rs,
		lk:             lk,
		analyticsModel: analyticsmodel.New(app, ds, rs, lk),
		natsService:    natsservice.New(app),
	}
}

func (m *DataMsgModel) SendDataMessage(r *plugnmeet.DataMessageReq) error {
	switch r.MsgBodyType {
	case plugnmeet.DataMsgBodyType_RAISE_HAND:
		return m.raiseHand(r)
	case plugnmeet.DataMsgBodyType_LOWER_HAND:
		return m.lowerHand(r)
	case plugnmeet.DataMsgBodyType_OTHER_USER_LOWER_HAND:
		return m.otherUserLowerHand(r)
	case plugnmeet.DataMsgBodyType_INFO,
		plugnmeet.DataMsgBodyType_ALERT:
		return m.sendNotification(r)
	default:
		return errors.New(r.MsgBodyType.String() + " yet not ready")
	}
}

func (m *DataMsgModel) deliverMsg(roomId string, destinationUserIds []string, msg *plugnmeet.DataMessage) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		log.Errorln(err)
		return err
	}

	// send as the push message
	_, err = m.lk.SendData(roomId, data, livekit.DataPacket_RELIABLE, destinationUserIds)
	if err != nil {
		log.Errorln(err)
		return err
	}

	return nil
}
