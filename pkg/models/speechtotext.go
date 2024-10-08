package models

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	log "github.com/sirupsen/logrus"
	"strconv"
	"time"
)

type SpeechToTextModel struct {
	app             *config.AppConfig
	ds              *dbservice.DatabaseService
	rs              *redisservice.RedisService
	analyticsModel  *AnalyticsModel
	webhookNotifier *helpers.WebhookNotifier
	natsService     *natsservice.NatsService
}

func NewSpeechToTextModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService) *SpeechToTextModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.New(app.DB)
	}
	if rs == nil {
		rs = redisservice.New(app.RDS)
	}

	return &SpeechToTextModel{
		app:             app,
		ds:              ds,
		rs:              rs,
		analyticsModel:  NewAnalyticsModel(app, ds, rs),
		webhookNotifier: helpers.GetWebhookNotifier(app),
		natsService:     natsservice.New(app),
	}
}

func (m *SpeechToTextModel) sendToWebhookNotifier(rId, rSid string, userId *string, task plugnmeet.SpeechServiceUserStatusTasks, usage int64) {
	tk := task.String()
	n := m.webhookNotifier
	if n == nil {
		return
	}
	msg := &plugnmeet.CommonNotifyEvent{
		Event: &tk,
		Room: &plugnmeet.NotifyEventRoom{
			Sid:    &rSid,
			RoomId: &rId,
		},
		SpeechService: &plugnmeet.SpeechServiceEvent{
			UserId:     userId,
			TotalUsage: usage,
		},
	}
	err := n.SendWebhookEvent(msg)
	if err != nil {
		log.Errorln(err)
	}
}

func (m *SpeechToTextModel) OnAfterRoomEnded(roomId, sId string) error {
	if sId == "" {
		return nil
	}
	// we'll wait a little bit to make sure all users' requested has been received
	time.Sleep(config.WaitBeforeSpeechServicesOnAfterRoomEnded)

	hkeys, err := m.rs.SpeechToTextGetHashKeys(roomId)
	if err != nil {
		return err
	}
	for _, k := range hkeys {
		if k != "total_usage" {
			_ = m.SpeechServiceUsersUsage(roomId, sId, k, plugnmeet.SpeechServiceUserStatusTasks_SPEECH_TO_TEXT_SESSION_ENDED)
		}
	}

	// send by webhook
	usage, _ := m.rs.SpeechToTextGetTotalUsageByRoomId(roomId)
	if usage != "" {
		c, err := strconv.ParseInt(usage, 10, 64)
		if err == nil {
			m.sendToWebhookNotifier(roomId, sId, nil, plugnmeet.SpeechServiceUserStatusTasks_SPEECH_TO_TEXT_TOTAL_USAGE, c)
			// send analytics
			m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
				EventType:        plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
				EventName:        plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_SPEECH_SERVICE_TOTAL_USAGE,
				RoomId:           roomId,
				EventValueString: &usage,
			})
		}
	}

	// now clean
	err = m.rs.SpeechToTextDeleteRoom(roomId)
	if err != nil {
		return err
	}

	return nil
}
