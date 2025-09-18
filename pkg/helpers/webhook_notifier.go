package helpers

import (
	"sync"
	"time"

	"github.com/goccy/go-json"
	"github.com/gofiber/fiber/v2/log"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/webhook"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
)

type WebhookNotifier struct {
	ds                   *dbservice.DatabaseService
	rs                   *redisservice.RedisService
	app                  *config.AppConfig
	natsService          *natsservice.NatsService
	isEnabled            bool
	enabledForPerMeeting bool
	defaultUrl           string
	// notifiers will hold a queue for each room, local to this server instance
	notifiers map[string]*webhook.Notifier
	// mu will protect access to the notifiers map
	mu     sync.Mutex
	logger *logrus.Entry
}

type webhookRedisFields struct {
	Urls            []string `json:"urls"`
	PerformDeleting bool     `json:"perform_deleting"`
}

func newWebhookNotifier(app *config.AppConfig, logger *logrus.Logger) *WebhookNotifier {
	w := &WebhookNotifier{
		app:                  app,
		ds:                   dbservice.New(app.DB, logger),
		natsService:          natsservice.New(app, logger),
		isEnabled:            app.Client.WebhookConf.Enable,
		enabledForPerMeeting: app.Client.WebhookConf.EnableForPerMeeting,
		defaultUrl:           app.Client.WebhookConf.Url,
		notifiers:            make(map[string]*webhook.Notifier),
		logger:               logger.WithField("helper", "webhookNotifier"),
	}

	// Subscribe to the cleanup broadcast channel for clustered environments.
	w.subscribeToCleanup()

	return w
}

// subscribeToCleanup listens for cleanup messages broadcast to all servers.
func (w *WebhookNotifier) subscribeToCleanup() {
	_, err := w.app.NatsConn.Subscribe(natsservice.WebhookCleanupSubject, func(m *nats.Msg) {
		roomId := string(m.Data)
		log.Infof("received cleanup signal for room: %s", roomId)
		w.cleanupNotifier(roomId)
	})
	if err != nil {
		log.Errorf("failed to subscribe to webhook cleanup subject: %v", err)
	}
}

// getOrCreateNotifier returns a dedicated notifier for a given room.
// If one doesn't exist, it creates and stores it.
func (w *WebhookNotifier) getOrCreateNotifier(roomId string) *webhook.Notifier {
	w.mu.Lock()
	defer w.mu.Unlock()

	if notifier, ok := w.notifiers[roomId]; ok {
		return notifier
	}

	// Create a new notifier for this room
	notifier := webhook.NewNotifier(config.DefaultWebhookQueueSize, w.app.Client.Debug, w.logger.Logger)
	w.notifiers[roomId] = notifier
	log.Infof("created new webhook queue for room: %s", roomId)

	return notifier
}

// cleanupNotifier stops the worker and removes the notifier for a room from the local map.
func (w *WebhookNotifier) cleanupNotifier(roomId string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if notifier, ok := w.notifiers[roomId]; ok {
		notifier.Kill() // This will call Kill() on the worker.
		delete(w.notifiers, roomId)
		log.Infof("cleaned up webhook queue for room: %s", roomId)
	}
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
		w.logger.WithError(err).Errorln("failed to save webhook data")
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
		// this meeting does not have any webhook url
		return nil
	}

	if !d.PerformDeleting {
		// this mean may be new session has been started for the same room
		return nil
	}

	// Broadcast a cleanup message to all servers in the cluster.
	// Only the server running the worker for this room will act on it.
	err = w.app.NatsConn.Publish(natsservice.WebhookCleanupSubject, []byte(roomId))
	if err != nil {
		log.Errorf("failed to publish webhook cleanup for room %s: %v", roomId, err)
	}

	return w.natsService.DeleteWebhookData(roomId)
}

func (w *WebhookNotifier) SendWebhookEvent(event *plugnmeet.CommonNotifyEvent) error {
	if !w.isEnabled || event.Room.GetRoomId() == "" {
		return nil
	}
	roomId := event.Room.GetRoomId()

	d, err := w.getData(roomId)
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
		err := w.saveData(roomId, d)
		if err != nil {
			// we'll just log
			w.logger.WithError(err).Errorln("failed to save webhook data")
		}
	} else if event.GetEvent() == "room_finished" && !d.PerformDeleting {
		// if we got room_finished then we'll set for deleting
		d.PerformDeleting = true
		err := w.saveData(roomId, d)
		if err != nil {
			// we'll just log
			w.logger.WithError(err).Errorln("failed to save webhook data")
		}
	}

	// Use the dedicated notifier for this room
	notifier := w.getOrCreateNotifier(roomId)
	notifier.AddInNotifyQueue(event, w.app.Client.ApiKey, w.app.Client.Secret, d.Urls)
	return nil
}

// ForceToPutInQueue sends a webhook event synchronously without using the room's queue.
// This method should be used for one-shot events outside the normal room lifecycle.
// It directly queries the database for webhook URLs.
func (w *WebhookNotifier) ForceToPutInQueue(event *plugnmeet.CommonNotifyEvent) {
	if !w.isEnabled {
		return
	}
	if event.Room.GetSid() == "" || event.Room.GetRoomId() == "" {
		w.logger.Errorln("empty room info for", event.GetEvent())
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

	notifier := webhook.NewNotifier(config.DefaultWebhookQueueSize, w.app.Client.Debug, w.logger.Logger)
	defer notifier.StopGracefully()
	notifier.AddInNotifyQueue(event, w.app.Client.ApiKey, w.app.Client.Secret, urls)
}

func (w *WebhookNotifier) saveData(roomId string, d *webhookRedisFields) error {
	marshal, err := json.Marshal(d)
	if err != nil {
		return err
	}

	// we'll simply override any existing value & put new
	err = w.natsService.AddWebhookData(roomId, marshal)
	if err != nil {
		return err
	}

	return nil
}

func (w *WebhookNotifier) getData(roomId string) (*webhookRedisFields, error) {
	data, err := w.natsService.GetWebhookData(roomId)
	if err != nil {
		return nil, err
	}

	if data == nil {
		return nil, nil
	}

	d := new(webhookRedisFields)
	err = json.Unmarshal(data, d)
	if err != nil {
		return nil, err
	}

	return d, nil
}

var webhookNotifier *WebhookNotifier

func GetWebhookNotifier(app *config.AppConfig, logger *logrus.Logger) *WebhookNotifier {
	if webhookNotifier != nil {
		return webhookNotifier
	}
	webhookNotifier = newWebhookNotifier(app, logger)

	return webhookNotifier
}
