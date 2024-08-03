package webhookmodel

import (
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/analyticsmodel"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/roommodel"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/dbservice"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekitservice"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redisservice"
	log "github.com/sirupsen/logrus"
)

type WebhookModel struct {
	app            *config.AppConfig
	ds             *dbservice.DatabaseService
	rs             *redisservice.RedisService
	lk             *livekitservice.LivekitService
	rm             *roommodel.RoomModel
	analyticsModel *analyticsmodel.AnalyticsModel
	notifier       *WebhookNotifier
}

func New(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, lk *livekitservice.LivekitService) *WebhookModel {
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

	return &WebhookModel{
		app:            app,
		ds:             ds,
		rs:             rs,
		lk:             lk,
		rm:             roommodel.New(app, ds, rs, lk),
		analyticsModel: analyticsmodel.New(app, ds, rs, lk),
		notifier:       getNotifier(ds, rs),
	}
}

func (m *WebhookModel) HandleWebhookEvents(e *livekit.WebhookEvent) {
	switch e.GetEvent() {
	case "room_started":
		m.roomStarted(e)
	case "room_finished":
		m.roomFinished(e)

		//case "participant_joined":
		//	m.participantJoined()
		//case "participant_left":
		//	m.participantLeft()
		//
		//case "track_published":
		//	m.trackPublished()
		//case "track_unpublished":
		//	m.trackUnpublished()
	}
}

func (m *WebhookModel) sendToWebhookNotifier(event *livekit.WebhookEvent) {
	if event == nil || m.notifier == nil {
		return
	}
	if event.Room == nil {
		log.Errorln("empty room info for event: ", event.GetEvent())
		return
	}

	msg := utils.PrepareCommonWebhookNotifyEvent(event)
	err := m.notifier.SendWebhookEvent(msg)
	if err != nil {
		log.Errorln(err)
	}
}

func (m *WebhookModel) sendCustomTypeWebhook(event *livekit.WebhookEvent, eventName string) {
	if event == nil || m.notifier == nil {
		return
	}
	if event.Room == nil {
		log.Errorln("empty room info for event: ", event.GetEvent())
		return
	}

	msg := utils.PrepareCommonWebhookNotifyEvent(event)
	msg.Event = &eventName
	err := m.notifier.SendWebhookEvent(msg)
	if err != nil {
		log.Errorln(err)
	}
}

func (m *WebhookModel) GetWebhookNotifier() *WebhookNotifier {
	return m.notifier
}
