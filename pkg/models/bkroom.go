package models

import (
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

type BreakoutRoomModel struct {
	rs             *redisservice.RedisService
	natsService    *natsservice.NatsService
	rm             *RoomModel
	analyticsModel *AnalyticsModel
	um             *UserModel
	logger         *logrus.Entry
}

func NewBreakoutRoomModel(rm *RoomModel) *BreakoutRoomModel {
	return &BreakoutRoomModel{
		rm:             rm,
		rs:             rm.rs,
		natsService:    rm.natsService,
		analyticsModel: rm.analyticsModel,
		um:             rm.userModel,
		logger:         rm.logger.Logger.WithField("model", "breakout_room"),
	}
}
