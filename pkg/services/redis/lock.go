package redisservice

import (
	"fmt"
	"time"
)

const (
	RoomCreationLockKey = Prefix + "roomCreationLock-%s"
	SchedulerLockKey    = Prefix + "schedulerLock-%s"
)

func (s *RedisService) LockRoomCreation(roomId string, ttl time.Duration) error {
	key := fmt.Sprintf(RoomCreationLockKey, roomId)
	_, err := s.rc.Set(s.ctx, key, fmt.Sprintf("%d", time.Now().Unix()), ttl).Result()
	if err != nil {
		return err
	}

	return nil
}

func (s *RedisService) IsRoomCreationLock(roomId string) bool {
	key := fmt.Sprintf(RoomCreationLockKey, roomId)
	result, err := s.rc.Get(s.ctx, key).Result()
	if err != nil {
		return false
	}

	if result != "" {
		return true
	}

	return false
}

func (s *RedisService) UnlockRoomCreation(roomId string) {
	_, _ = s.rc.Del(s.ctx, fmt.Sprintf(RoomCreationLockKey, roomId)).Result()
}

func (s *RedisService) LockSchedulerTask(task string, ttl time.Duration) error {
	key := fmt.Sprintf(SchedulerLockKey, task)
	_, err := s.rc.Set(s.ctx, key, fmt.Sprintf("%d", time.Now().Unix()), ttl).Result()
	if err != nil {
		return err
	}

	return nil
}

func (s *RedisService) IsSchedulerTaskLock(task string) bool {
	key := fmt.Sprintf(SchedulerLockKey, task)
	result, err := s.rc.Get(s.ctx, key).Result()
	if err != nil {
		return false
	}

	if result != "" {
		return true
	}

	return false
}

func (s *RedisService) UnlockSchedulerTask(task string) {
	_, _ = s.rc.Del(s.ctx, fmt.Sprintf(SchedulerLockKey, task)).Result()
}
