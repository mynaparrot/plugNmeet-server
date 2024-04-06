package models

import (
	"context"
	"github.com/goccy/go-json"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/webhook"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
	"time"
)

const (
	defaultQueueSize = 200
	WebhookRedisKey  = "pnm:webhookData"
	authHeader       = "Authorization"
	hashToken        = "Hash-Token" // in various Apache modules will strip the Authorization header,
	// so we'll use additional one
)

type WebhookNotifier struct {
	ctx                  context.Context
	rc                   *redis.Client
	rm                   *RoomModel
	isEnabled            bool
	enabledForPerMeeting bool
	defaultUrl           string
	notifier             *webhook.Notifier
}

type webhookRedisFields struct {
	Urls            []string `json:"urls"`
	PerformDeleting bool     `json:"perform_deleting"`
}

func newWebhookNotifier() *WebhookNotifier {
	notifier := webhook.GetWebhookNotifier(100, config.AppCnf.Client.Debug, config.GetLogger())

	w := &WebhookNotifier{
		ctx:                  context.Background(),
		rc:                   config.AppCnf.RDS,
		rm:                   NewRoomModel(),
		isEnabled:            config.AppCnf.Client.WebhookConf.Enable,
		enabledForPerMeeting: config.AppCnf.Client.WebhookConf.EnableForPerMeeting,
		defaultUrl:           config.AppCnf.Client.WebhookConf.Url,
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
		roomInfo, _ := w.rm.GetRoomInfo("", sid, 0)
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

	_, err = w.rc.HDel(w.ctx, WebhookRedisKey, roomId).Result()
	return err
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

	w.notifier.AddInNotifyQueue(event, config.AppCnf.Client.ApiKey, config.AppCnf.Client.Secret, d.Urls)
	return nil
}

// ForceToPutInQueue can be used to force checking meeting table to get url
// this method will not do further validation. We should not use this method always because fetching data mysql will be slower than redis
func (w *WebhookNotifier) ForceToPutInQueue(event *plugnmeet.CommonNotifyEvent) {
	if !w.isEnabled {
		return
	}
	if event.Room.GetSid() == "" || event.Room.GetRoomId() == "" {
		return
	}

	var urls []string
	if w.defaultUrl != "" {
		urls = append(urls, w.defaultUrl)
	}

	if w.enabledForPerMeeting {
		roomInfo, _ := w.rm.GetRoomInfo("", event.Room.GetSid(), 0)
		if roomInfo != nil {
			if roomInfo.WebhookUrl != "" {
				urls = append(urls, roomInfo.WebhookUrl)
			}
		}
	}

	if len(urls) < 1 {
		return
	}

	w.notifier.AddInNotifyQueue(event, config.AppCnf.Client.ApiKey, config.AppCnf.Client.Secret, urls)
}

func (w *WebhookNotifier) saveData(roomId string, d *webhookRedisFields) error {
	marshal, err := json.Marshal(d)
	if err != nil {
		return err
	}

	// we'll simply overrider any existing value & put new
	_, err = w.rc.HSet(w.ctx, WebhookRedisKey, roomId, marshal).Result()
	if err != nil {
		return err
	}

	return nil
}

func (w *WebhookNotifier) getData(roomId string) (*webhookRedisFields, error) {
	result, err := w.rc.HGet(w.ctx, WebhookRedisKey, roomId).Result()
	switch {
	case err == redis.Nil:
		return nil, nil
	case err != nil:
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

func GetWebhookNotifier() *WebhookNotifier {
	if webhookNotifier != nil {
		return webhookNotifier
	}
	webhookNotifier = newWebhookNotifier()

	return webhookNotifier
}
