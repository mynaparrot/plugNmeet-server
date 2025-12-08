package redisservice

import (
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	RoomWithDurationInfoKey = Prefix + "roomWithDurationInfo"
)

func (s *RedisService) AddRoomWithDurationInfo(roomId string, vals interface{}) error {
	key := fmt.Sprintf("%s:%s", RoomWithDurationInfoKey, roomId)

	pipe := s.rc.Pipeline()
	pipe.HSet(s.ctx, key, vals)
	pipe.Expire(s.ctx, key, time.Hour*24)
	_, err := pipe.Exec(s.ctx)
	if err != nil {
		return err
	}
	return nil
}

func (s *RedisService) SetRoomDuration(roomId, durationField string, val uint64) error {
	key := fmt.Sprintf("%s:%s", RoomWithDurationInfoKey, roomId)

	pipe := s.rc.Pipeline()
	pipe.HSet(s.ctx, key, durationField, val)
	pipe.Expire(s.ctx, key, time.Hour*24)
	_, err := pipe.Exec(s.ctx)
	if err != nil {
		return err
	}
	return nil
}

func (s *RedisService) UpdateRoomDuration(roomId, durationField string, duration uint64) (int64, error) {
	return s.rc.HIncrBy(s.ctx, fmt.Sprintf("%s:%s", RoomWithDurationInfoKey, roomId), durationField, int64(duration)).Result()
}

func (s *RedisService) GetRoomWithDurationInfo(roomId string, dest interface{}) error {
	err := s.rc.HGetAll(s.ctx, fmt.Sprintf("%s:%s", RoomWithDurationInfoKey, roomId)).Scan(dest)
	switch {
	case errors.Is(err, redis.Nil):
		return nil
	case err != nil:
		return err
	}
	return nil
}

func (s *RedisService) GetRoomWithDurationInfoByKey(key string, dest interface{}) error {
	err := s.rc.HGetAll(s.ctx, key).Scan(dest)
	switch {
	case errors.Is(err, redis.Nil):
		return nil
	case err != nil:
		return err
	}
	return nil
}

func (s *RedisService) GetRoomsWithDurationKeys() ([]string, error) {
	return s.rc.Keys(s.ctx, RoomWithDurationInfoKey+":*").Result()
}

func (s *RedisService) DeleteRoomWithDuration(roomId string) error {
	_, err := s.rc.Del(s.ctx, fmt.Sprintf("%s:%s", RoomWithDurationInfoKey, roomId)).Result()
	if err != nil {
		return err
	}
	return nil
}
