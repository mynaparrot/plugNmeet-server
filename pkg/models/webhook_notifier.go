package models

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/frostbyte73/core"
	"github.com/goccy/go-json"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/livekit/protocol/auth"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/encoding/protojson"
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
	client               *retryablehttp.Client
	isEnabled            bool
	enabledForPerMeeting bool
	defaultUrl           string
	dropped              atomic.Int32
	worker               core.QueueWorker
}

type webhookRedisFields struct {
	Urls            []string `json:"urls"`
	PerformDeleting bool     `json:"progress_deleting"`
}

func NewWebhookNotifier() *WebhookNotifier {
	client := retryablehttp.NewClient()
	client.Logger = nil

	w := &WebhookNotifier{
		ctx:                  context.Background(),
		rc:                   config.AppCnf.RDS,
		rm:                   NewRoomModel(),
		isEnabled:            config.AppCnf.Client.WebhookConf.Enable,
		enabledForPerMeeting: config.AppCnf.Client.WebhookConf.EnableForPerMeeting,
		defaultUrl:           config.AppCnf.Client.WebhookConf.Url,
		client:               client,
	}

	w.worker = core.NewQueueWorker(core.QueueWorkerParams{
		QueueSize:    defaultQueueSize,
		DropWhenFull: true,
		OnDropped: func() {
			l := w.dropped.Inc()
			log.Println(fmt.Sprintf("Total dropped webhook events: %d", l))
		},
	})

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
		if roomInfo != nil {
			if roomInfo.WebhookUrl != "" {
				urls = append(urls, roomInfo.WebhookUrl)
			}
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

	w.addInNotifyQueue(event, d.Urls)
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

	w.addInNotifyQueue(event, urls)
}

func (w *WebhookNotifier) addInNotifyQueue(event *plugnmeet.CommonNotifyEvent, urls []string) {
	if !w.isEnabled || len(urls) < 1 {
		return
	}

	for _, u := range urls {
		w.worker.Submit(func() {
			err := w.sendPostRequest(event, u)
			if err != nil {
				log.Errorln("failed to sendPostRequest webhook,", "url:", u, "event:", event.GetEvent(), "roomId:", event.GetRoom().GetRoomId(), "sid:", event.Room.GetSid(), "error:", err)
			} else {
				if config.AppCnf.Client.Debug {
					log.Println("webhook sent for event:", event.GetEvent(), "roomID:", event.Room.GetRoomId(), "sid:", event.Room.GetSid(), "to URL:", u)
				}
			}
		})
	}
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

func (w *WebhookNotifier) sendPostRequest(event *plugnmeet.CommonNotifyEvent, url string) error {
	op := protojson.MarshalOptions{
		EmitUnpopulated: false,
		UseProtoNames:   true,
	}
	encoded, err := op.Marshal(event)
	if err != nil {
		return err
	}
	// sign payload
	sum := sha256.Sum256(encoded)
	b64 := base64.StdEncoding.EncodeToString(sum[:])

	at := auth.NewAccessToken(config.AppCnf.Client.ApiKey, config.AppCnf.Client.Secret).
		SetValidFor(5 * time.Minute).
		SetSha256(b64)
	token, err := at.ToJWT()
	if err != nil {
		return err
	}
	r, err := retryablehttp.NewRequest("POST", url, bytes.NewReader(encoded))
	if err != nil {
		// ignore and continue
		return err
	}
	r.Header.Set(authHeader, token)
	// in various Apache modules will strip the Authorization header,
	// so we'll use additional one for easy use
	r.Header.Set(hashToken, token)
	// use a custom mime type to ensure the signature is checked prior to parsing
	r.Header.Set("content-type", "application/webhook+json")
	res, err := w.client.Do(r)
	if err != nil {
		return err
	}
	_ = res.Body.Close()

	statusOK := res.StatusCode >= 200 && res.StatusCode < 300
	if !statusOK {
		return errors.New(fmt.Sprintf("http response code: %d, msg: %s", res.StatusCode, res.Status))
	}
	return nil
}

var webhookNotifier *WebhookNotifier

func GetWebhookNotifier() *WebhookNotifier {
	if webhookNotifier != nil {
		return webhookNotifier
	}
	webhookNotifier = NewWebhookNotifier()

	return webhookNotifier
}
