package models

import (
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	log "github.com/sirupsen/logrus"
)

type WebhookModel struct {
	app             *config.AppConfig
	ds              *dbservice.DatabaseService
	rs              *redisservice.RedisService
	rm              *RoomModel
	analyticsModel  *AnalyticsModel
	webhookNotifier *helpers.WebhookNotifier
	natsService     *natsservice.NatsService
}

func NewWebhookModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService) *WebhookModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.New(app.DB)
	}
	if rs == nil {
		rs = redisservice.New(app.RDS)
	}

	return &WebhookModel{
		app:             app,
		ds:              ds,
		rs:              rs,
		rm:              NewRoomModel(app, ds, rs),
		analyticsModel:  NewAnalyticsModel(app, ds, rs),
		webhookNotifier: helpers.GetWebhookNotifier(app),
		natsService:     natsservice.New(app),
	}
}

func (m *WebhookModel) HandleWebhookEvents(e *livekit.WebhookEvent) {
	switch e.GetEvent() {
	case "room_started":
		m.roomStarted(e)
	case "room_finished":
		m.roomFinished(e)

	case "participant_joined":
		m.participantJoined(e)
	case "participant_left":
		m.participantLeft(e)

	case "track_published":
		m.trackPublished(e)
	case "track_unpublished":
		m.trackUnpublished(e)
	}
}

func (m *WebhookModel) sendToWebhookNotifier(event *livekit.WebhookEvent) {
	if event == nil || m.webhookNotifier == nil {
		return
	}
	if event.Room == nil {
		log.Errorln("empty room info for event: ", event.GetEvent())
		return
	}

	msg := utils.PrepareCommonWebhookNotifyEvent(event)
	err := m.webhookNotifier.SendWebhookEvent(msg)
	if err != nil {
		log.Errorln(err)
	}
}

func (m *WebhookModel) sendCustomTypeWebhook(event *livekit.WebhookEvent, eventName string) {
	if event == nil || m.webhookNotifier == nil {
		return
	}
	if event.Room == nil {
		log.Errorln("empty room info for event: ", event.GetEvent())
		return
	}

	msg := utils.PrepareCommonWebhookNotifyEvent(event)
	msg.Event = &eventName
	err := m.webhookNotifier.SendWebhookEvent(msg)
	if err != nil {
		log.Errorln(err)
	}
}
