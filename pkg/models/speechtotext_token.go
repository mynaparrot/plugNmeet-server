package models

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"io"
	"net/http"
	"sort"
	"strconv"
)

func (m *SpeechToTextModel) GenerateAzureToken(r *plugnmeet.GenerateAzureTokenReq, requestedUserId string) error {
	e, err := m.rs.SpeechToTextAzureKeyRequestedTask(r.RoomId, requestedUserId, "check")
	if err != nil {
		return err
	}
	if e == "exist" {
		return errors.New("speech-services.already-received-token")
	}

	// check if this user already using service or not
	ss, err := m.rs.SpeechToTextCheckUserUsage(r.RoomId, requestedUserId)
	if err != nil {
		return err
	}
	if ss != "" {
		return errors.New("speech-services.already-using-service")
	}

	meta, err := m.natsService.GetRoomMetadataStruct(r.RoomId)
	if err != nil {
		return err
	}
	f := meta.RoomFeatures.SpeechToTextTranslationFeatures

	if !m.app.AzureCognitiveServicesSpeech.Enabled || !f.IsEnabled {
		return errors.New("speech-services.service-disabled")
	}

	k, err := m.selectAzureKey()
	if err != nil {
		return err
	}

	res, err := m.sendRequestToAzureForToken(k.SubscriptionKey, k.ServiceRegion, k.Id)
	if err != nil {
		return err
	}
	// we'll store this user'analyticsModel info
	_, err = m.rs.SpeechToTextAzureKeyRequestedTask(r.RoomId, requestedUserId, "add")
	if err != nil {
		return err
	}

	return m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_AZURE_COGNITIVE_SERVICE_SPEECH_TOKEN, r.RoomId, res, &requestedUserId)
}

func (m *SpeechToTextModel) RenewAzureToken(r *plugnmeet.AzureTokenRenewReq, requestedUserId string) error {
	ss, err := m.rs.SpeechToTextCheckUserUsage(r.RoomId, requestedUserId)
	if err != nil {
		return err
	}

	if ss == "" {
		return errors.New("speech-services.renew-need-already-using-service")
	}

	sub := m.app.AzureCognitiveServicesSpeech.SubscriptionKeys
	var key string
	for _, s := range sub {
		if s.Id == r.KeyId {
			key = s.SubscriptionKey
			continue
		}
	}
	if key == "" {
		return errors.New("speech-services.renew-subscription-key-not-found")
	}

	res, err := m.sendRequestToAzureForToken(key, r.ServiceRegion, r.KeyId)
	if err != nil {
		return err
	}

	// send token by data channel
	res.Renew = true
	return m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_AZURE_COGNITIVE_SERVICE_SPEECH_TOKEN, r.RoomId, res, &requestedUserId)
}

func (m *SpeechToTextModel) sendRequestToAzureForToken(subscriptionKey, serviceRegion, keyId string) (*plugnmeet.GenerateAzureTokenRes, error) {
	url := fmt.Sprintf("https://%s.api.cognitive.microsoft.com/sts/v1.0/issueToken", serviceRegion)
	r, err := http.NewRequest("POST", url, bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Ocp-Apim-Subscription-Key", subscriptionKey)
	r.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		return nil, err
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	token := string(body)
	return &plugnmeet.GenerateAzureTokenRes{
		Status:        true,
		Msg:           "success",
		Token:         &token,
		ServiceRegion: &serviceRegion,
		KeyId:         &keyId,
	}, nil
}

func (m *SpeechToTextModel) selectAzureKey() (*config.AzureSubscriptionKey, error) {
	sub := m.app.AzureCognitiveServicesSpeech.SubscriptionKeys

	if len(sub) == 0 {
		return nil, errors.New("no key found")
	} else if len(sub) == 1 {
		return &sub[0], nil
	}

	var keys []config.AzureSubscriptionKey
	for _, k := range sub {
		var err error
		conns, err := m.rs.SpeechToTextGetConnectionsByKeyId(k.Id)
		if err != nil {
			continue
		}

		var count int
		if conns != "" {
			count, err = strconv.Atoi(conns)
			if err != nil {
				continue
			}
		}

		k.MaxConnection = k.MaxConnection - int64(count)
		keys = append(keys, k)
	}

	if len(keys) == 0 {
		return nil, errors.New("no key found")
	}

	sort.Slice(keys, func(i int, j int) bool {
		return keys[i].MaxConnection > keys[j].MaxConnection
	})

	return &keys[0], nil
}
