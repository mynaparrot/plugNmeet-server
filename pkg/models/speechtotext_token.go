package models

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
)

func (m *SpeechToTextModel) GenerateAzureToken(r *plugnmeet.GenerateAzureTokenReq, requestedUserId string) error {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": r.RoomId,
		"userId": requestedUserId,
		"method": "GenerateAzureToken",
	})
	log.Infoln("request to generate azure token")

	e, err := m.rs.SpeechToTextAzureKeyRequestedTask(r.RoomId, requestedUserId, "check")
	if err != nil {
		log.WithError(err).Errorln("failed to check azure key requested task")
		return err
	}
	if e == "exist" {
		err = fmt.Errorf("speech-services.already-received-token")
		log.WithError(err).Warnln("user already received token")
		return err
	}

	// check if this user already using service or not
	ss, err := m.rs.SpeechToTextCheckUserUsage(r.RoomId, requestedUserId)
	if err != nil {
		log.WithError(err).Errorln("failed to check user usage")
		return err
	}
	if ss != "" {
		err = fmt.Errorf("speech-services.already-using-service")
		log.WithError(err).Warnln("user already using service")
		return err
	}

	meta, err := m.natsService.GetRoomMetadataStruct(r.RoomId)
	if err != nil {
		log.WithError(err).Errorln("failed to get room metadata")
		return err
	}
	if meta == nil {
		err = fmt.Errorf("invalid nil room metadata information")
		log.WithError(err).Errorln()
		return err
	}
	f := meta.RoomFeatures.SpeechToTextTranslationFeatures

	if !m.app.AzureCognitiveServicesSpeech.Enabled || !f.IsEnabled {
		err = fmt.Errorf("speech-services.service-disabled")
		log.WithError(err).Warnln("speech service is disabled")
		return err
	}

	k, err := m.selectAzureKey()
	if err != nil {
		log.WithError(err).Errorln("failed to select azure key")
		return err
	}

	res, err := m.sendRequestToAzureForToken(k.SubscriptionKey, k.ServiceRegion, k.Id)
	if err != nil {
		log.WithError(err).Errorln("failed to get token from azure")
		return err
	}
	// we'll store this user'analyticsModel info
	_, err = m.rs.SpeechToTextAzureKeyRequestedTask(r.RoomId, requestedUserId, "add")
	if err != nil {
		log.WithError(err).Errorln("failed to store user requested task")
		return err
	}

	log.Info("successfully generated azure token, broadcasting to user")
	return m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_AZURE_COGNITIVE_SERVICE_SPEECH_TOKEN, r.RoomId, res, &requestedUserId)
}

func (m *SpeechToTextModel) RenewAzureToken(r *plugnmeet.AzureTokenRenewReq, requestedUserId string) error {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": r.RoomId,
		"userId": requestedUserId,
		"keyId":  r.KeyId,
		"method": "RenewAzureToken",
	})
	log.Infoln("request to renew azure token")

	ss, err := m.rs.SpeechToTextCheckUserUsage(r.RoomId, requestedUserId)
	if err != nil {
		log.WithError(err).Errorln("failed to check user usage")
		return err
	}

	if ss == "" {
		err = fmt.Errorf("speech-services.renew-need-already-using-service")
		log.WithError(err).Warnln("can't renew as user is not using service")
		return err
	}

	sub := m.app.AzureCognitiveServicesSpeech.SubscriptionKeys
	var key string
	for _, s := range sub {
		if s.Id == r.KeyId {
			key = s.SubscriptionKey
			break
		}
	}
	if key == "" {
		err = fmt.Errorf("speech-services.renew-subscription-key-not-found")
		log.WithError(err).Errorln("subscription key not found")
		return err
	}

	res, err := m.sendRequestToAzureForToken(key, r.ServiceRegion, r.KeyId)
	if err != nil {
		log.WithError(err).Errorln("failed to get token from azure")
		return err
	}

	// send token by data channel
	res.Renew = true
	log.Info("successfully renewed azure token, broadcasting to user")
	return m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_AZURE_COGNITIVE_SERVICE_SPEECH_TOKEN, r.RoomId, res, &requestedUserId)
}

func (m *SpeechToTextModel) sendRequestToAzureForToken(subscriptionKey, serviceRegion, keyId string) (*plugnmeet.GenerateAzureTokenRes, error) {
	log := m.logger.WithFields(logrus.Fields{
		"serviceRegion": serviceRegion,
		"keyId":         keyId,
		"method":        "sendRequestToAzureForToken",
	})

	url := fmt.Sprintf("https://%s.api.cognitive.microsoft.com/sts/v1.0/issueToken", serviceRegion)
	r, err := http.NewRequest("POST", url, bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Ocp-Apim-Subscription-Key", subscriptionKey)
	r.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		log.WithError(err).Errorln("http client execution failed")
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("received non-200 status code: %d", resp.StatusCode)
		log.WithError(err).Errorln("azure request failed")
		return nil, err
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.WithError(err).Errorln("failed to read response body")
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
	log := m.logger.WithField("method", "selectAzureKey")
	log.Infoln("selecting an azure key")

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
			log.WithError(err).WithField("keyId", k.Id).Warnln("could not get connections for key")
			continue
		}

		var count int
		if conns != "" {
			count, err = strconv.Atoi(conns)
			if err != nil {
				log.WithError(err).WithField("keyId", k.Id).Warnln("could not parse connections count")
				continue
			}
		}

		k.MaxConnection = k.MaxConnection - int64(count)
		keys = append(keys, k)
	}

	if len(keys) == 0 {
		err := fmt.Errorf("no usable azure key found after checking connections")
		log.WithError(err).Errorln()
		return nil, err
	}

	sort.Slice(keys, func(i int, j int) bool {
		return keys[i].MaxConnection > keys[j].MaxConnection
	})

	log.WithField("selectedKeyId", keys[0].Id).Infoln("selected azure key")
	return &keys[0], nil
}
