package helpers

import (
	"github.com/goccy/go-json"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/webhook"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/dbservice"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redisservice"
	log "github.com/sirupsen/logrus"
	"time"
)

type WebhookNotifier struct {
	ds                   *dbservice.DatabaseService
	rs                   *redisservice.RedisService
	app                  *config.AppConfig
	isEnabled            bool
	enabledForPerMeeting bool
	defaultUrl           string
	notifier             *webhook.Notifier
}

type webhookRedisFields struct {
	Urls            []string `json:"urls"`
	PerformDeleting bool     `json:"perform_deleting"`
}

func newWebhookNotifier(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService) *WebhookNotifier {
	notifier := webhook.GetWebhookNotifier(config.DefaultWebhookQueueSize, app.Client.Debug, config.GetLogger())

	w := &WebhookNotifier{
		ds:                   ds,
		rs:                   rs,
		app:                  app,
		isEnabled:            app.Client.WebhookConf.Enable,
		enabledForPerMeeting: app.Client.WebhookConf.EnableForPerMeeting,
		defaultUrl:           app.Client.WebhookConf.Url,
		notifier:             notifier,
	}

	return w
}

func (w *WebhookNotifier) RegisterWebhook(roomId, sid string) {
	if !w.isEnabled || roomId == "" {
		return
	}

	var urls []string
	if w.defaultUrl != "" {
		urls = append(urls, w.defaultUrl)
	}

	if w.enabledForPerMeeting {
		roomInfo, _ := w.ds.GetRoomInfoBySid(sid, nil)
		if roomInfo != nil && roomInfo.WebhookUrl != "" {
			urls = append(urls, roomInfo.WebhookUrl)
		}
	}

	if len(urls) < 1 {
		return
	}

	d := &webhookRedisFields{
		Urls:            urls,
		PerformDeleting: false,
	}

	err := w.saveData(roomId, d)
	if err != nil {
		log.Errorln(err)
	}
}

func (w *WebhookNotifier) DeleteWebhook(roomId string) error {
	// we'll wait long time before delete WebhookQueued
	// to make sure that we've completed everything else
	time.Sleep(config.MaxDurationWaitBeforeCleanRoomWebhook)

	d, err := w.getData(roomId)
	if err != nil {
		return err
	}
	if d == nil {
		// this meeting do not have any webhook url
		return nil
	}

	if !d.PerformDeleting {
		// this mean may be new session has been started for same room
		return nil
	}

	return w.rs.DeleteWebhookData(roomId)
}

func (w *WebhookNotifier) SendWebhookEvent(event *plugnmeet.CommonNotifyEvent) error {
	if !w.isEnabled || event.Room.GetRoomId() == "" {
		return nil
	}

	d, err := w.getData(event.Room.GetRoomId())
	if err != nil {
		return err
	}
	if d == nil {
		return nil
	}

	// it may happen that the room was created again before we delete the queue
	// in DeleteWebhook
	// if we delete then no further events will sendPostRequest even the room is active,
	// so here we'll reset the deleted status
	if event.GetEvent() == "room_started" && d.PerformDeleting {
		d.PerformDeleting = false
		err := w.saveData(event.Room.GetRoomId(), d)
		if err != nil {
			// we'll just log
			log.Errorln(err)
		}
	} else if event.GetEvent() == "room_finished" && !d.PerformDeleting {
		// if we got room_finished then we'll set for deleting
		d.PerformDeleting = true
		err := w.saveData(event.Room.GetRoomId(), d)
		if err != nil {
			// we'll just log
			log.Errorln(err)
		}
	}

	w.notifier.AddInNotifyQueue(event, w.app.Client.ApiKey, w.app.Client.Secret, d.Urls)
	return nil
}

// ForceToPutInQueue can be used to force checking meeting table to get url
// this method will not do further validation. We should not use this method always because fetching data mysql will be slower than redis
func (w *WebhookNotifier) ForceToPutInQueue(event *plugnmeet.CommonNotifyEvent) {
	if !w.isEnabled {
		return
	}
	if event.Room.GetSid() == "" || event.Room.GetRoomId() == "" {
		log.Errorln("empty room info for", event.GetEvent())
		return
	}

	var urls []string
	if w.defaultUrl != "" {
		urls = append(urls, w.defaultUrl)
	}

	if w.enabledForPerMeeting {
		roomInfo, _ := w.ds.GetRoomInfoBySid(event.Room.GetSid(), nil)
		if roomInfo != nil && roomInfo.WebhookUrl != "" {
			urls = append(urls, roomInfo.WebhookUrl)
		}
	}

	if len(urls) < 1 {
		return
	}

	w.notifier.AddInNotifyQueue(event, w.app.Client.ApiKey, w.app.Client.Secret, urls)
}

func (w *WebhookNotifier) saveData(roomId string, d *webhookRedisFields) error {
	marshal, err := json.Marshal(d)
	if err != nil {
		return err
	}

	// we'll simply override any existing value & put new
	err = w.rs.AddWebhookData(roomId, marshal)
	if err != nil {
		return err
	}

	return nil
}

func (w *WebhookNotifier) getData(roomId string) (*webhookRedisFields, error) {
	result, err := w.rs.GetWebhookData(roomId)
	if err != nil {
		return nil, err
	}

	if result == "" {
		return nil, nil
	}

	d := new(webhookRedisFields)
	err = json.Unmarshal([]byte(result), d)
	if err != nil {
		return nil, err
	}

	return d, nil
}

var webhookNotifier *WebhookNotifier

func GetWebhookNotifier(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService) *WebhookNotifier {
	if webhookNotifier != nil {
		return webhookNotifier
	}
	webhookNotifier = newWebhookNotifier(app, ds, rs)

	return webhookNotifier
}
