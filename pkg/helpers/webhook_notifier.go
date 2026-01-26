package helpers

import (
	"context"
	"sync"
	"time"

	"github.com/goccy/go-json"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/webhook"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
)

// roomNotifier holds a dedicated worker and the cached webhook URLs for a single room.
type roomNotifier struct {
	worker *webhook.Notifier
	urls   []string
}

type WebhookNotifier struct {
	ctx                  context.Context
	ds                   *dbservice.DatabaseService
	rs                   *redisservice.RedisService
	app                  *config.AppConfig
	natsService          *natsservice.NatsService
	isEnabled            bool
	enabledForPerMeeting bool
	defaultUrl           string
	// notifiers will hold our new wrapper for each room
	notifiers map[string]*roomNotifier
	// mu will protect access to the notifiers map
	mu     sync.Mutex
	logger *logrus.Entry
}

type webhookRedisFields struct {
	Urls            []string `json:"urls"`
	PerformDeleting bool     `json:"perform_deleting"`
}

func newWebhookNotifier(ctx context.Context, app *config.AppConfig, ds *dbservice.DatabaseService, natsService *natsservice.NatsService, logger *logrus.Logger) *WebhookNotifier {
	w := &WebhookNotifier{
		ctx:                  ctx,
		app:                  app,
		ds:                   ds,
		natsService:          natsService,
		isEnabled:            app.Client.WebhookConf.Enable,
		enabledForPerMeeting: app.Client.WebhookConf.EnableForPerMeeting,
		defaultUrl:           app.Client.WebhookConf.Url,
		notifiers:            make(map[string]*roomNotifier),
		logger:               logger.WithField("helper", "webhookNotifier"),
	}

	// Subscribe to the cleanup broadcast channel for clustered environments.
	w.subscribeToCleanup()

	return w
}

// subscribeToCleanup listens for cleanup messages broadcast to all servers.
func (w *WebhookNotifier) subscribeToCleanup() {
	_, err := w.app.NatsConn.Subscribe(natsservice.WebhookCleanupSubject, func(m *nats.Msg) {
		roomId := string(m.Data) // Make a copy of the data
		w.logger.WithFields(logrus.Fields{
			"room_id": roomId,
			"method":  "subscribeToCleanup",
		}).Info("received webhook cleanup signal")
		w.cleanupNotifier(roomId)
	})
	if err != nil {
		w.logger.WithFields(logrus.Fields{
			"method": "subscribeToCleanup",
		}).WithError(err).Error("failed to subscribe to webhook cleanup subject")
	}
}

// getOrCreateNotifier gets a notifier for a room, creating it just-in-time if it doesn't exist on this server instance.
func (w *WebhookNotifier) getOrCreateNotifier(roomId string) *roomNotifier {
	w.mu.Lock()
	notifier, ok := w.notifiers[roomId]
	w.mu.Unlock()

	if ok {
		return notifier
	}

	// Notifier does not exist on this server instance. Create it.
	d, err := w.getData(roomId)
	if err != nil || d == nil || len(d.Urls) == 0 {
		// Log the error but don't create a notifier if there are no URLs.
		if err != nil {
			w.logger.WithField("room_id", roomId).WithError(err).Error("failed to get webhook data for notifier creation")
		}
		return nil
	}

	// Create the new wrapper with the fetched URLs and the worker.
	newNotifier := &roomNotifier{
		worker: webhook.NewNotifier(w.ctx, config.DefaultWebhookQueueSize, w.logger.Logger),
		urls:   d.Urls,
	}

	// Lock again to safely add to the map.
	w.mu.Lock()
	defer w.mu.Unlock()

	// Double-check in case another goroutine created it in the meantime.
	if existingNotifier, exists := w.notifiers[roomId]; exists {
		// Another goroutine won the race. Discard our new one and use the existing one.
		newNotifier.worker.Kill()
		return existingNotifier
	}

	w.notifiers[roomId] = newNotifier
	w.logger.WithField("room_id", roomId).Info("created new just-in-time webhook notifier for room")
	return newNotifier
}

// cleanupNotifier stops the worker and removes the notifier for a room from the local map.
func (w *WebhookNotifier) cleanupNotifier(roomId string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if notifier, ok := w.notifiers[roomId]; ok {
		notifier.worker.Kill() // This will call Kill() on the worker.
		delete(w.notifiers, roomId)
		w.logger.WithFields(logrus.Fields{
			"room_id": roomId,
			"method":  "cleanupNotifier",
		}).Info("cleaned up webhook queue for room")
	}
}

func (w *WebhookNotifier) RegisterWebhook(roomId, sid string) {
	log := w.logger.WithFields(logrus.Fields{
		"room_id": roomId,
		"sid":     sid,
		"method":  "RegisterWebhook",
	})
	log.Info("request to register webhook")

	if !w.isEnabled {
		log.Debug("webhook is disabled, skipping registration")
		return
	}
	if roomId == "" {
		log.Warn("room_id is empty, skipping registration")
		return
	}

	var urls []string
	if w.defaultUrl != "" {
		urls = append(urls, w.defaultUrl)
		log.WithField("default_url", w.defaultUrl).Debug("added default webhook url")
	}

	if w.enabledForPerMeeting {
		roomInfo, _ := w.ds.GetRoomInfoBySid(sid, nil)
		if roomInfo != nil && roomInfo.WebhookUrl != "" {
			urls = append(urls, roomInfo.WebhookUrl)
			log.WithField("per_meeting_url", roomInfo.WebhookUrl).Debug("added per-meeting webhook url")
		}
	}

	if len(urls) < 1 {
		log.Info("no webhook urls found to register")
		return
	}
	log.WithField("urls", urls).Info("found webhook urls to register")

	d := &webhookRedisFields{
		Urls:            urls,
		PerformDeleting: false,
	}

	err := w.saveData(roomId, d)
	if err != nil {
		log.WithError(err).Errorln("failed to save webhook data")
		return
	}
	log.Info("successfully registered webhook")
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
		w.logger.WithFields(logrus.Fields{
			"room_id": roomId,
			"method":  "DeleteWebhook",
		}).WithError(err).Error("failed to publish webhook cleanup")
	}

	return w.natsService.DeleteWebhookData(roomId)
}

func (w *WebhookNotifier) SendWebhookEvent(event *plugnmeet.CommonNotifyEvent) error {
	if !w.isEnabled || event.Room.GetRoomId() == "" {
		return nil
	}
	roomId := event.Room.GetRoomId()

	// Handle state changes for room_started and room_finished
	if event.GetEvent() == "room_started" || event.GetEvent() == "room_finished" {
		d, err := w.getData(roomId)
		if err != nil {
			return err
		}
		if d != nil {
			if event.GetEvent() == "room_started" && d.PerformDeleting {
				d.PerformDeleting = false
				_ = w.saveData(roomId, d)
			} else if event.GetEvent() == "room_finished" && !d.PerformDeleting {
				d.PerformDeleting = true
				_ = w.saveData(roomId, d)
			}
		}
	}

	// Use the clean, separated getOrCreateNotifier function.
	notifier := w.getOrCreateNotifier(roomId)
	if notifier == nil {
		// This is expected condition if no URLs are configured.
		return nil
	}

	// Send the event to the queue using the static app config and the cached URLs.
	notifier.worker.AddInNotifyQueue(event, w.app.Client.ApiKey, w.app.Client.Secret, notifier.urls)
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

	notifier := webhook.NewNotifier(w.ctx, config.DefaultWebhookQueueSize, w.logger.Logger)
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

func GetWebhookNotifier(ctx context.Context, app *config.AppConfig, ds *dbservice.DatabaseService, natsService *natsservice.NatsService, logger *logrus.Logger) *WebhookNotifier {
	if webhookNotifier != nil {
		return webhookNotifier
	}
	webhookNotifier = newWebhookNotifier(ctx, app, ds, natsService, logger)

	return webhookNotifier
}
