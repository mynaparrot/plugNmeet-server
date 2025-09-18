package models

import (
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/sirupsen/logrus"
)

type BreakoutRoomModel struct {
	natsService    *natsservice.NatsService
	rm             *RoomModel
	rDuration      *RoomDurationModel
	analyticsModel *AnalyticsModel
	um             *UserModel
	logger         *logrus.Entry
}

func NewBreakoutRoomModel(rm *RoomModel, natsService *natsservice.NatsService) *BreakoutRoomModel {
	return &BreakoutRoomModel{
		rm:             rm,
		natsService:    natsService,
		rDuration:      rm.roomDuration,
		analyticsModel: rm.analyticsModel,
		um:             rm.userModel,
		logger:         rm.logger.Logger.WithField("model", "breakout_room"),
	}
}
