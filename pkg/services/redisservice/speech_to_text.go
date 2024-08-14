package redisservice

import (
	"errors"
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/redis/go-redis/v9"
	"strconv"
	"time"
)

const SpeechServiceRedisKey = "pnm:speechService"

func (s *RedisService) SpeechToTextGetConnectionsByKeyId(keyId string) (string, error) {
	keyStatus := fmt.Sprintf("%s:%s:connections", SpeechServiceRedisKey, keyId)
	conns, err := s.rc.Get(s.ctx, keyStatus).Result()
	switch {
	case errors.Is(err, redis.Nil):
		return "", nil
	case err != nil:
		return "", err
	}

	return conns, nil
}

func (s *RedisService) SpeechToTextUpdateUserStatus(keyId string, task plugnmeet.SpeechServiceUserStatusTasks) error {
	keyStatus := fmt.Sprintf("%s:%s:connections", SpeechServiceRedisKey, keyId)
	switch task {
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

	return nil
}

func (s *RedisService) SpeechToTextCheckUserUsage(roomId, userId string) (string, error) {
	key := fmt.Sprintf("%s:%s:usage", SpeechServiceRedisKey, roomId)

	ss, err := s.rc.HGet(s.ctx, key, userId).Result()
	switch {
	case errors.Is(err, redis.Nil):
		return "", nil
	case err != nil:
		return "", err
	}

	return ss, nil
}

func (s *RedisService) SpeechToTextUsersUsage(roomId, userId string, task plugnmeet.SpeechServiceUserStatusTasks) (int64, error) {
	key := fmt.Sprintf("%s:%s:usage", SpeechServiceRedisKey, roomId)
	ss, err := s.SpeechToTextCheckUserUsage(roomId, userId)
	if err != nil {
		return 0, err
	}

	switch task {
	case plugnmeet.SpeechServiceUserStatusTasks_SPEECH_TO_TEXT_SESSION_STARTED:
		_, err := s.rc.HSet(s.ctx, key, userId, time.Now().Unix()).Result()
		if err != nil {
			return 0, err
		}
	case plugnmeet.SpeechServiceUserStatusTasks_SPEECH_TO_TEXT_SESSION_ENDED:
		start, err := strconv.Atoi(ss)
		if err != nil {
			return 0, err
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
			return 0, err
		}
		return usage, nil
	}

	return 0, nil
}

func (s *RedisService) SpeechToTextAzureKeyRequestedTask(roomId, userId string, task string) (string, error) {
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

func (s *RedisService) SpeechToTextGetHashKeys(roomId string) ([]string, error) {
	key := fmt.Sprintf("%s:%s:usage", SpeechServiceRedisKey, roomId)
	hkeys, err := s.rc.HKeys(s.ctx, key).Result()
	switch {
	case errors.Is(err, redis.Nil):
		return nil, nil
	case err != nil:
		return nil, err
	}

	return hkeys, nil
}
func (s *RedisService) SpeechToTextGetTotalUsageByRoomId(roomId string) (string, error) {
	key := fmt.Sprintf("%s:%s:usage", SpeechServiceRedisKey, roomId)
	return s.rc.HGet(s.ctx, key, "total_usage").Result()
}

func (s *RedisService) SpeechToTextDeleteRoom(roomId string) error {
	key := fmt.Sprintf("%s:%s:usage", SpeechServiceRedisKey, roomId)
	_, err := s.rc.Del(s.ctx, key).Result()
	if err != nil {
		return err
	}
	return nil
}
