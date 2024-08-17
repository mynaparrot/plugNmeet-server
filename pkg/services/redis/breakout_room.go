package redisservice

import (
	"errors"
	"github.com/redis/go-redis/v9"
)

const breakoutRoomKey = Prefix + "breakoutRoom:"

func (s *RedisService) InsertOrUpdateBreakoutRoom(roomId string, val map[string]string) error {
	pp := s.rc.Pipeline()
	pp.HSet(s.ctx, breakoutRoomKey+roomId, val)
	_, err := pp.Exec(s.ctx)
	if err != nil {
		return err
	}
	return nil
}

func (s *RedisService) DeleteBreakoutRoom(roomId, breakoutRoomId string) error {
	_, err := s.rc.HDel(s.ctx, breakoutRoomKey+roomId, breakoutRoomId).Result()
	if err != nil {
		return err
	}
	return nil
}

func (s *RedisService) GetBreakoutRoom(roomId, breakoutRoomId string) (string, error) {
	result, err := s.rc.HGet(s.ctx, breakoutRoomKey+roomId, breakoutRoomId).Result()
	switch {
	case errors.Is(err, redis.Nil):
		return "", nil
	case err != nil:
		return "", err
	}

	return result, nil
}

func (s *RedisService) CountBreakoutRooms(roomId string) (int64, error) {
	return s.rc.HLen(s.ctx, breakoutRoomKey+roomId).Result()
}

func (s *RedisService) GetAllBreakoutRoomsByParentRoomId(roomId string) (map[string]string, error) {
	return s.rc.HGetAll(s.ctx, breakoutRoomKey+roomId).Result()
}

func (s *RedisService) DeleteAllBreakoutRoomsByParentRoomId(roomId string) error {
	_, err := s.rc.Del(s.ctx, breakoutRoomKey+roomId).Result()
	if err != nil {
		return err
	}
	return nil
}
