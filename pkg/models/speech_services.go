package models

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
	"io"
	"net/http"
	"sort"
	"strconv"
	"time"
)

type SpeechServices struct {
	rc          *redis.Client
	ctx         context.Context
	roomService *RoomService
}

const SpeechServiceRedisKey = "pnm:speechService"

func NewSpeechServices() *SpeechServices {
	return &SpeechServices{
		rc:          config.AppCnf.RDS,
		ctx:         context.Background(),
		roomService: NewRoomService(),
	}
}

func (s *SpeechServices) SpeechToTextTranslationReq(r *plugnmeet.SpeechToTextTranslationReq) error {
	if !config.AppCnf.AzureCognitiveServicesSpeech.Enabled {
		return errors.New("speech service disabled")
	}

	_, meta, err := s.roomService.LoadRoomWithMetadata(r.RoomId)
	if err != nil {
		return err
	}

	f := meta.RoomFeatures.SpeechToTextTranslationFeatures

	f.IsEnabled = r.IsEnabled
	f.AllowedSpeechLangs = r.AllowedSpeechLangs
	f.AllowedSpeechUsers = r.AllowedSpeechUsers

	f.IsEnabledTranslation = r.IsEnabledTranslation
	f.AllowedTransLangs = r.AllowedTransLangs

	_, err = s.roomService.UpdateRoomMetadataByStruct(r.RoomId, meta)
	if err != nil {
		return err
	}

	return nil
}

func (s *SpeechServices) GenerateAzureToken(r *plugnmeet.GenerateAzureTokenReq, requestedUserId string) error {
	e, err := s.azureKeyRequestedTask(r.RoomId, requestedUserId, "check")
	if err != nil {
		return err
	}
	if e == "exist" {
		return errors.New("speech-services.already-received-token")
	}

	// check if this user already using service or not
	ss, err := s.checkUserUsage(r.RoomId, requestedUserId)
	if err != nil {
		return err
	}
	if ss != "" {
		return errors.New("speech-services.already-using-service")
	}

	_, meta, err := s.roomService.LoadRoomWithMetadata(r.RoomId)
	if err != nil {
		return err
	}
	f := meta.RoomFeatures.SpeechToTextTranslationFeatures

	if !config.AppCnf.AzureCognitiveServicesSpeech.Enabled || !f.IsEnabled {
		return errors.New("speech-services.service-disabled")
	}

	res, err := s.sendRequestToAzureForToken()
	if err != nil {
		return err
	}
	// we'll store this user's info
	_, err = s.azureKeyRequestedTask(r.RoomId, requestedUserId, "add")
	if err != nil {
		return err
	}

	// send token by data channel
	marshal, err := protojson.Marshal(res)
	if err != nil {
		return err
	}
	dm := NewDataMessageModel()
	err = dm.SendDataMessage(&plugnmeet.DataMessageReq{
		RoomId:      r.RoomId,
		UserSid:     "system",
		MsgBodyType: plugnmeet.DataMsgBodyType_AZURE_COGNITIVE_SERVICE_SPEECH_TOKEN,
		Msg:         string(marshal),
		SendTo:      []string{r.UserSid},
	})

	return err
}

func (s *SpeechServices) SpeechServiceUserStatus(r *plugnmeet.SpeechServiceUserStatusReq) error {
	keyStatus := fmt.Sprintf("%s:%s:connections", SpeechServiceRedisKey, r.KeyId)

	switch r.Task {
	case plugnmeet.SpeechServiceUserStatusTasks_SPEECH_TO_TEXT_SESSION_STARTED:
		_, err := s.rc.Incr(s.ctx, keyStatus).Result()
		if err != nil {
			return err
		}
	case plugnmeet.SpeechServiceUserStatusTasks_SPEECH_TO_TEXT_SESSION_ENDED:
		_, err := s.rc.Decr(s.ctx, keyStatus).Result()
		if err != nil {
			return err
		}
	}

	return s.SpeechServiceUsersUsage(r.RoomId, r.RoomSid, r.UserId, r.Task)
}

func (s *SpeechServices) SpeechServiceUsersUsage(roomId, rSid, userId string, task plugnmeet.SpeechServiceUserStatusTasks) error {
	key := fmt.Sprintf("%s:%s:usage", SpeechServiceRedisKey, roomId)

	ss, err := s.checkUserUsage(roomId, userId)
	if err != nil {
		return err
	}

	switch task {
	case plugnmeet.SpeechServiceUserStatusTasks_SPEECH_TO_TEXT_SESSION_STARTED:
		if ss == "" {
			_, err := s.rc.HSet(s.ctx, key, userId, time.Now().Unix()).Result()
			if err != nil {
				return err
			}
		}
		// webhook
		s.sendToWebhookNotifier(roomId, rSid, userId, task, 0)
	case plugnmeet.SpeechServiceUserStatusTasks_SPEECH_TO_TEXT_SESSION_ENDED:
		if ss != "" {
			start, err := strconv.Atoi(ss)
			if err != nil {
				return err
			}
			now := time.Now().Unix()
			var usage int64
			err = s.rc.Watch(s.ctx, func(tx *redis.Tx) error {
				_, err := tx.Pipelined(s.ctx, func(pipeliner redis.Pipeliner) error {
					usage = now - int64(start)
					pipeliner.HIncrBy(s.ctx, key, "total_usage", usage).Result()
					pipeliner.HDel(s.ctx, key, userId).Result()
					return nil
				})
				return err
			}, key)

			if err != nil {
				return err
			}
			// send webhook
			s.sendToWebhookNotifier(roomId, rSid, userId, task, usage)
		}
	}

	// now remove this user from request list
	_, _ = s.azureKeyRequestedTask(roomId, userId, "remove")
	return nil
}

func (s *SpeechServices) OnAfterRoomEnded(roomId, sId string) {
	key := fmt.Sprintf("%s:%s:usage", SpeechServiceRedisKey, roomId)
	hkeys, err := s.rc.HKeys(s.ctx, key).Result()
	switch {
	case err == redis.Nil:
		//
	case err != nil:
		return
	}

	for _, k := range hkeys {
		if k != "total_usage" {
			s.SpeechServiceUsersUsage(roomId, sId, k, plugnmeet.SpeechServiceUserStatusTasks_SPEECH_TO_TEXT_SESSION_ENDED)
		}
	}

	// send by webhook
	usage, _ := s.rc.HGet(s.ctx, key, "total_usage").Result()
	if usage != "" {
		c, err := strconv.Atoi(usage)
		if err == nil {
			s.sendToWebhookNotifier(roomId, sId, "", plugnmeet.SpeechServiceUserStatusTasks_SPEECH_TO_TEXT_TOTAL_USAGE, int64(c))
		}
	}
	// now clean
	s.rc.Del(s.ctx, key).Result()
}

func (s *SpeechServices) sendRequestToAzureForToken() (*plugnmeet.GenerateAzureTokenRes, error) {
	k, err := s.selectAzureKey()
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("https://%s.api.cognitive.microsoft.com/sts/v1.0/issueToken", k.ServiceRegion)
	r, err := http.NewRequest("POST", url, bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Ocp-Apim-Subscription-Key", k.SubscriptionKey)
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
		ServiceRegion: &k.ServiceRegion,
		KeyId:         &k.Id,
	}, nil
}

func (s *SpeechServices) checkUserUsage(roomId, userId string) (string, error) {
	key := fmt.Sprintf("%s:%s:usage", SpeechServiceRedisKey, roomId)

	ss, err := s.rc.HGet(s.ctx, key, userId).Result()
	switch {
	case err == redis.Nil:
		//
	case err != nil:
		return "", err
	}

	return ss, nil
}

func (s *SpeechServices) azureKeyRequestedTask(roomId, userId string, task string) (string, error) {
	key := fmt.Sprintf("%s:%s:%s:azureKeyRequested", SpeechServiceRedisKey, roomId, userId)

	switch task {
	case "check":
		e, err := s.rc.Get(s.ctx, key).Result()
		switch {
		case err == redis.Nil:
			return "", nil
		case err != nil:
			return "", err
		}
		if e != "" {
			return "exist", nil
		}
	case "add":
		_, err := s.rc.Set(s.ctx, key, userId, 5*time.Minute).Result()
		if err != nil {
			return "", err
		}
	case "remove":
		_, err := s.rc.Del(s.ctx, key).Result()
		if err != nil {
			return "", err
		}
	}
	return "", nil
}

func (s *SpeechServices) selectAzureKey() (*config.AzureSubscriptionKey, error) {
	sub := config.AppCnf.AzureCognitiveServicesSpeech.SubscriptionKeys
	if len(sub) == 0 {
		return nil, errors.New("no key found")
	} else if len(sub) == 1 {
		return &sub[0], nil
	}

	var keys []config.AzureSubscriptionKey
	for _, k := range sub {
		keyStatus := fmt.Sprintf("%s:%s:connections", SpeechServiceRedisKey, k.Id)

		conns, err := s.rc.Get(s.ctx, keyStatus).Result()
		switch {
		case err == redis.Nil:
			keys = append(keys, k)
		case err != nil:
			continue
		}

		c, err := strconv.Atoi(conns)
		if err != nil {
			continue
		}

		k.MaxConnection = k.MaxConnection - int64(c)
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

func (s *SpeechServices) sendToWebhookNotifier(rId, rSid, userId string, task plugnmeet.SpeechServiceUserStatusTasks, usage int64) {
	tk := task.String()
	n := NewWebhookNotifier()
	msg := &plugnmeet.CommonNotifyEvent{
		Event: &tk,
		Room: &plugnmeet.NotifyEventRoom{
			Sid:    &rId,
			RoomId: &rSid,
		},
		SpeechService: &plugnmeet.SpeechServiceEvent{
			UserId:     &userId,
			TotalUsage: usage,
		},
	}
	err := n.Notify(rSid, msg)
	if err != nil {
		log.Errorln(err)
	}
}
