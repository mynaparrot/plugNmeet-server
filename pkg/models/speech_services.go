package models

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/redis/go-redis/v9"
	"io"
	"net/http"
	"strconv"
	"time"
)

type SpeechServices struct {
	rc          *redis.Client
	ctx         context.Context
	roomService *RoomService
}

const SpeechServiceRedisKey = "pnm:speechService:"

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

func (s *SpeechServices) GenerateAzureToken(r *plugnmeet.GenerateTokenReq) (*plugnmeet.GenerateAzureTokenRes, error) {
	_, meta, err := s.roomService.LoadRoomWithMetadata(r.RoomId)
	if err != nil {
		return nil, err
	}
	f := meta.RoomFeatures.SpeechToTextTranslationFeatures

	if !config.AppCnf.AzureCognitiveServicesSpeech.Enabled || !f.IsEnabled {
		return nil, errors.New("speech service disabled")
	}

	res, err := s.sendRequestToAzureForToken()
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (s *SpeechServices) SpeechServiceUserStatus(r *plugnmeet.SpeechServiceUserStatusReq) error {
	keyStatus := fmt.Sprintf("%s:%s:connections", SpeechServiceRedisKey, r.KeyId)

	switch r.Task {
	case plugnmeet.SpeechServiceUserStatusTasks_SESSION_STARTED:
		s.rc.Incr(s.ctx, keyStatus)
	case plugnmeet.SpeechServiceUserStatusTasks_SESSION_ENDED:
		s.rc.Decr(s.ctx, keyStatus)
	}

	return s.SpeechServiceUsersUsage(r.RoomId, r.UserId, r.Task)
}

func (s *SpeechServices) SpeechServiceUsersUsage(roomId, userId string, task plugnmeet.SpeechServiceUserStatusTasks) error {
	key := fmt.Sprintf("%s:%s:usage", SpeechServiceRedisKey, roomId)
	ss, err := s.rc.HGet(s.ctx, key, userId).Result()
	switch {
	case err == redis.Nil:
		//
	case err != nil:
		return err
	}

	switch task {
	case plugnmeet.SpeechServiceUserStatusTasks_SESSION_STARTED:
		if ss == "" {
			_, err := s.rc.HSet(s.ctx, key, userId, time.Now().UnixMilli()).Result()
			if err != nil {
				return err
			}
		}
	case plugnmeet.SpeechServiceUserStatusTasks_SESSION_ENDED:
		if ss != "" {
			start, err := strconv.Atoi(ss)
			if err != nil {
				return err
			}
			now := time.Now().UnixMilli()
			_, err = s.rc.HIncrBy(s.ctx, key, "total_usage", now-int64(start)).Result()
			if err != nil {
				return err
			}
		}
		_, err = s.rc.HDel(s.ctx, key, userId).Result()
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *SpeechServices) sendRequestToAzureForToken() (*plugnmeet.GenerateAzureTokenRes, error) {
	sub := config.AppCnf.AzureCognitiveServicesSpeech.SubscriptionKeys
	if len(sub) == 0 {
		return nil, errors.New("no key found")
	}
	// TODO: think a better way to select key
	// TODO: At present just use the first one
	k := sub[0]
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
