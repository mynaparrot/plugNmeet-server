package models

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

type RoomModel struct {
	app            *config.AppConfig
	ds             *dbservice.DatabaseService
	rs             *redisservice.RedisService
	lk             *livekitservice.LivekitService
	natsService    *natsservice.NatsService
	logger         *logrus.Entry
	userModel      *UserModel
	recorderModel  *RecorderModel
	fileModel      *FileModel
	roomDuration   *RoomDurationModel
	etherpadModel  *EtherpadModel
	pollModel      *PollModel
	speechToText   *SpeechToTextModel
	analyticsModel *AnalyticsModel
}

func NewRoomModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, lk *livekitservice.LivekitService, natsService *natsservice.NatsService, userModel *UserModel, recorderModel *RecorderModel, fileModel *FileModel, roomDuration *RoomDurationModel, etherpadModel *EtherpadModel, pollModel *PollModel, speechToText *SpeechToTextModel, analyticsModel *AnalyticsModel, logger *logrus.Logger) *RoomModel {
	return &RoomModel{
		app:            app,
		ds:             ds,
		rs:             rs,
		lk:             lk,
		natsService:    natsService,
		userModel:      userModel,
		recorderModel:  recorderModel,
		fileModel:      fileModel,
		roomDuration:   roomDuration,
		etherpadModel:  etherpadModel,
		pollModel:      pollModel,
		speechToText:   speechToText,
		analyticsModel: analyticsModel,
		logger:         logger.WithField("model", "room"),
	}
}
