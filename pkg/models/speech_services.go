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
	rc             *redis.Client
	ctx            context.Context
	roomService    *RoomService
	analyticsModel *AnalyticsModel
}

const SpeechServiceRedisKey = "pnm:speechService"

func NewSpeechServices() *SpeechServices {
	return &SpeechServices{
		rc:             config.AppCnf.RDS,
		ctx:            context.Background(),
		roomService:    NewRoomService(),
		analyticsModel: NewAnalyticsModel(),
	}
}

func (s *SpeechServices) SpeechToTextTranslationServiceStatus(r *plugnmeet.SpeechToTextTranslationReq) error {
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
	f.DefaultSubtitleLang = r.DefaultSubtitleLang

	_, err = s.roomService.UpdateRoomMetadataByStruct(r.RoomId, meta)
	if err != nil {
		return err
	}

	// send analytics
	val := plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_STARTED.String()
	d := &plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_SPEECH_SERVICE_STATUS,
		RoomId:    r.RoomId,
		HsetValue: &val,
	}
	if !f.IsEnabled {
		val = plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_ENDED.String()
		d.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_SPEECH_SERVICE_STATUS
		d.HsetValue = &val
	}
	s.analyticsModel.HandleEvent(d)

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

	k, err := s.selectAzureKey()
	if err != nil {
		return err
	}

	res, err := s.sendRequestToAzureForToken(k.SubscriptionKey, k.ServiceRegion, k.Id)
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
	return dm.SendDataMessage(&plugnmeet.DataMessageReq{
		RoomId:      r.RoomId,
		UserSid:     "system",
		MsgBodyType: plugnmeet.DataMsgBodyType_AZURE_COGNITIVE_SERVICE_SPEECH_TOKEN,
		Msg:         string(marshal),
		SendTo:      []string{requestedUserId},
	})
}

func (s *SpeechServices) RenewAzureToken(r *plugnmeet.AzureTokenRenewReq, requestedUserId string) error {
	ss, err := s.checkUserUsage(r.RoomId, requestedUserId)
	if err != nil {
		return err
	}

	if ss == "" {
		return errors.New("speech-services.renew-need-already-using-service")
	}

	sub := config.AppCnf.AzureCognitiveServicesSpeech.SubscriptionKeys
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

	res, err := s.sendRequestToAzureForToken(key, r.ServiceRegion, r.KeyId)
	if err != nil {
		return err
	}

	// send token by data channel
	res.Renew = true
	marshal, err := protojson.Marshal(res)
	if err != nil {
		return err
	}

	dm := NewDataMessageModel()
	return dm.SendDataMessage(&plugnmeet.DataMessageReq{
		RoomId:      r.RoomId,
		UserSid:     "system",
		MsgBodyType: plugnmeet.DataMsgBodyType_AZURE_COGNITIVE_SERVICE_SPEECH_TOKEN,
		Msg:         string(marshal),
		SendTo:      []string{requestedUserId},
	})
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
		s.sendToWebhookNotifier(roomId, rSid, &userId, task, 0)
		// send analytics
		val := plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_STARTED.String()
		s.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
			EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_USER,
			EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_SPEECH_SERVICES_STATUS,
			RoomId:    roomId,
			UserId:    &userId,
			HsetValue: &val,
		})
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
			s.sendToWebhookNotifier(roomId, rSid, &userId, task, usage)
			// send analytics
			val := plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_ENDED.String()
			s.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
				EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_USER,
				EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_SPEECH_SERVICES_STATUS,
				RoomId:    roomId,
				UserId:    &userId,
				HsetValue: &val,
			})
			// another to record total usage
			s.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
				EventType:         plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_USER,
				EventName:         plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_SPEECH_SERVICES_USAGE,
				RoomId:            roomId,
				UserId:            &userId,
				EventValueInteger: &usage,
			})
		}
	}

	// now remove this user from request list
	_, _ = s.azureKeyRequestedTask(roomId, userId, "remove")
	return nil
}

func (s *SpeechServices) OnAfterRoomEnded(roomId, sId string) {
	if sId == "" {
		return
	}
	// we'll wait a little bit to make sure all users' requested has been received
	time.Sleep(config.WaitBeforeSpeechServicesOnAfterRoomEnded)

	key := fmt.Sprintf("%s:%s:usage", SpeechServiceRedisKey, roomId)
	hkeys, err := s.rc.HKeys(s.ctx, key).Result()
	switch {
	case errors.Is(err, redis.Nil):
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
		c, err := strconv.ParseInt(usage, 10, 64)
		if err == nil {
			s.sendToWebhookNotifier(roomId, sId, nil, plugnmeet.SpeechServiceUserStatusTasks_SPEECH_TO_TEXT_TOTAL_USAGE, c)
			// send analytics
			s.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
				EventType:        plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
				EventName:        plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_SPEECH_SERVICE_TOTAL_USAGE,
				RoomId:           roomId,
				EventValueString: &usage,
			})
		}
	}
	// now clean
	s.rc.Del(s.ctx, key).Result()
}

func (s *SpeechServices) sendRequestToAzureForToken(subscriptionKey, serviceRegion, keyId string) (*plugnmeet.GenerateAzureTokenRes, error) {
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

func (s *SpeechServices) checkUserUsage(roomId, userId string) (string, error) {
	key := fmt.Sprintf("%s:%s:usage", SpeechServiceRedisKey, roomId)

	ss, err := s.rc.HGet(s.ctx, key, userId).Result()
	switch {
	case errors.Is(err, redis.Nil):
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
		case errors.Is(err, redis.Nil):
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
		case errors.Is(err, redis.Nil):
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

func (s *SpeechServices) sendToWebhookNotifier(rId, rSid string, userId *string, task plugnmeet.SpeechServiceUserStatusTasks, usage int64) {
	tk := task.String()
	n := GetWebhookNotifier(rId, rSid)
	if n == nil {
		return
	}
	msg := &plugnmeet.CommonNotifyEvent{
		Event: &tk,
		Room: &plugnmeet.NotifyEventRoom{
			Sid:    &rSid,
			RoomId: &rId,
		},
		SpeechService: &plugnmeet.SpeechServiceEvent{
			UserId:     userId,
			TotalUsage: usage,
		},
	}
	err := n.SendWebhook(msg, nil)
	if err != nil {
		log.Errorln(err)
	}
}
