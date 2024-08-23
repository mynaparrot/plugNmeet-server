package redisservice

import (
	"errors"
	"fmt"
	"time"
)

const (
	RoomCreationProgressKey = Prefix + "roomCreationProgressList"
)

// RoomCreationProgressList can be used during a room creation
// we have seen that during create room in livekit an instant webhook sent
// from livekit but from our side we are still in progress,
// so it's better we'll wait before processing
// task = add | exist | del
func (s *RedisService) RoomCreationProgressList(roomId, task string) (bool, error) {
	key := fmt.Sprintf("%s:%s", RoomCreationProgressKey, roomId)
	switch task {
	case "add":
		// we'll set maximum 1 minute after that key will expire
		// this way we can ensure that there will not be any deadlock
		// otherwise in various reason key may stay in redis & create deadlock
		_, err := s.rc.Set(s.ctx, key, roomId, time.Minute*1).Result()
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
