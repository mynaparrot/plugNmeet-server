package models

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"github.com/goccy/go-json"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugNmeet/pkg/config"
	log "github.com/sirupsen/logrus"
	"net/http"
	"time"
)

// here just modified *livekit.WebhookEvent for our case
type CommonNotifyEvent struct {
	Event         string                   `json:"event,omitempty"`
	Room          NotifyEventRoom          `json:"room,omitempty"`
	Participant   *livekit.ParticipantInfo `json:"participant,omitempty"`
	RecordingInfo RecordingInfoEvent       `json:"recordingInfo,omitempty"`
	EgressInfo    *livekit.EgressInfo      `json:"egress_info,omitempty"`
	Track         *livekit.TrackInfo       `json:"track,omitempty"`
	Id            string                   `json:"id,omitempty"`
	CreatedAt     int64                    `json:"created_at,omitempty"`
}

type NotifyEventRoom struct {
	Sid             string           `json:"sid,omitempty"`
	RoomId          string           `json:"room_id,omitempty"`
	EmptyTimeout    uint32           `json:"empty_timeout,omitempty"`
	MaxParticipants uint32           `json:"max_participants,omitempty"`
	CreationTime    int64            `json:"creation_time,omitempty"`
	EnabledCodecs   []*livekit.Codec `json:"enabled_codecs,omitempty"`
	Metadata        string           `json:"metadata,omitempty"`
	NumParticipants uint32           `json:"num_participants,omitempty"`
}

type RecordingInfoEvent struct {
	RecordId    string  `json:"record_id"`
	RecorderId  string  `json:"recorder_id"`
	RecorderMsg string  `json:"recorder_msg"`
	FilePath    string  `json:"file_path,omitempty"`
	FileSize    float64 `json:"file_size,omitempty"`
}

type notifier struct {
	apiKey      string
	apiSecret   string
	urls        []string
	webhookConf config.WebhookConf
	roomModel   *roomModel
}

func NewWebhookNotifier() *notifier {
	return &notifier{
		apiKey:      config.AppCnf.Client.ApiKey,
		apiSecret:   config.AppCnf.Client.Secret,
		webhookConf: config.AppCnf.Client.WebhookConf,
		roomModel:   NewRoomModel(),
	}
}

func (n *notifier) Notify(roomSid string, msg interface{}) error {
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

func (n *notifier) _notify(msg interface{}) error {
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
