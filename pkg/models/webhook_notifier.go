package models

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"github.com/goccy/go-json"
	"github.com/livekit/protocol/auth"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	log "github.com/sirupsen/logrus"
	"net/http"
	"time"
)

type WebhookNotifierModel struct {
	apiKey      string
	apiSecret   string
	urls        []string
	webhookConf config.WebhookConf
	roomModel   *RoomModel
}

func NewWebhookNotifier() *WebhookNotifierModel {
	return &WebhookNotifierModel{
		apiKey:      config.AppCnf.Client.ApiKey,
		apiSecret:   config.AppCnf.Client.Secret,
		webhookConf: config.AppCnf.Client.WebhookConf,
		roomModel:   NewRoomModel(),
	}
}

func (n *WebhookNotifierModel) Notify(roomSid string, msg interface{}) error {
	if !n.webhookConf.Enable {
		return nil
	}

	if n.webhookConf.Url != "" {
		n.urls = append(n.urls, n.webhookConf.Url)
	}

	if n.webhookConf.EnableForPerMeeting {
		// if we set roomSid then it will avoid the value of isRunning
		roomInfo, _ := n.roomModel.GetRoomInfo("", roomSid, 0)
		if roomInfo.WebhookUrl != "" {
			n.urls = append(n.urls, roomInfo.WebhookUrl)
		}
	}

	if len(n.urls) > 0 {
		err := n._notify(msg)

		if err != nil {
			return err
		}
	}

	return nil
}

func (n *WebhookNotifierModel) _notify(msg interface{}) error {
	var encoded []byte
	var err error

	encoded, err = json.Marshal(msg)
	if err != nil {
		return err
	}

	// sign payload
	sum := sha256.Sum256(encoded)
	b64 := base64.StdEncoding.EncodeToString(sum[:])

	at := auth.NewAccessToken(n.apiKey, n.apiSecret).
		SetValidFor(5 * time.Minute).
		SetSha256(b64)
	token, err := at.ToJWT()

	if err != nil {
		return err
	}

	for _, url := range n.urls {
		r, err := http.NewRequest("POST", url, bytes.NewReader(encoded))
		if err != nil {
			// ignore and continue
			log.Errorln(err, "could not create request", "url", url)
			continue
		}
		r.Header.Set("Authorization", token)
		r.Header.Set("content-type", "application/json")
		_, err = http.DefaultClient.Do(r)
		if err != nil {
			log.Errorln(err, "could not post to webhook", "url", url)
		}
	}

	return nil
}
