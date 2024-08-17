package redisservice

import (
	"errors"
	"fmt"
	"time"
)

const (
	SchedulerLockKey = Prefix + "schedulerLock"
)

// ManageSchedulerLock will lock the work it will perform
// this way we can ensure one work was running in one server
// task = add | exist | del
func (s *RedisService) ManageSchedulerLock(task, workName string, expire time.Duration) (bool, error) {
	key := fmt.Sprintf("%s:%s", SchedulerLockKey, workName)
	switch task {
	case "add":
		_, err := s.rc.Set(s.ctx, key, time.Now().Unix(), expire).Result()
		if err != nil {
			return false, err
		}
		return true, nil
	case "exist":
		result, err := s.rc.Exists(s.ctx, key).Result()
		if err != nil {
			return false, err
		}
		if result > 0 {
			return true, nil
		}
		return false, nil
	case "del":
		_, err := s.rc.Del(s.ctx, key).Result()
		if err != nil {
			return false, err
		}
		return true, nil
	}

	return false, errors.New("invalid task")
}
