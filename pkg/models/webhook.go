package models

import (
	"context"

	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	livekitservice "github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

type WebhookModel struct {
	ctx             context.Context
	app             *config.AppConfig
	ds              *dbservice.DatabaseService
	rs              *redisservice.RedisService
	lk              *livekitservice.LivekitService
	rm              *RoomModel
	analyticsModel  *AnalyticsModel
	rmDuration      *RoomDurationModel
	bm              *BreakoutRoomModel
	nm              *NatsModel
	webhookNotifier *helpers.WebhookNotifier
	natsService     *natsservice.NatsService
	logger          *logrus.Entry
}

func NewWebhookModel(ctx context.Context, app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, natsService *natsservice.NatsService, lk *livekitservice.LivekitService, rm *RoomModel, analyticsModel *AnalyticsModel, rmDuration *RoomDurationModel, bm *BreakoutRoomModel, nm *NatsModel, webhookNotifier *helpers.WebhookNotifier, logger *logrus.Logger) *WebhookModel {
	return &WebhookModel{
		ctx:             ctx,
		app:             app,
		ds:              ds,
		rs:              rs,
		lk:              lk,
		rm:              rm,
		analyticsModel:  analyticsModel,
		rmDuration:      rmDuration,
		bm:              bm,
		nm:              nm,
		webhookNotifier: webhookNotifier,
		natsService:     natsService,
		logger:          logger.WithField("model", "webhook"),
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
		m.logger.Errorln("empty room info for event: ", event.GetEvent())
		return
	}

	msg := utils.PrepareCommonWebhookNotifyEvent(event)
	err := m.webhookNotifier.SendWebhookEvent(msg)
	if err != nil {
		m.logger.Errorln(err)
	}
}

func (m *WebhookModel) sendCustomTypeWebhook(event *livekit.WebhookEvent, eventName string) {
	if event == nil || m.webhookNotifier == nil {
		return
	}
	if event.Room == nil {
		m.logger.Errorln("empty room info for event: ", event.GetEvent())
		return
	}

	msg := utils.PrepareCommonWebhookNotifyEvent(event)
	msg.Event = &eventName
	err := m.webhookNotifier.SendWebhookEvent(msg)
	if err != nil {
		m.logger.Errorln(err)
	}
}
