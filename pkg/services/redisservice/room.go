package redisservice

import (
	"errors"
	"fmt"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
	"time"
)

const (
	ActiveRoomsWithMetadataKey = Prefix + "activeRoomsWithMetadata"
	RoomCreationProgressKey    = Prefix + "roomCreationProgressList"
)

// ManageActiveRoomsWithMetadata will use redis sorted active rooms with their metadata
// task = add | del | get | fetchAll
func (s *RedisService) ManageActiveRoomsWithMetadata(roomId, task, metadata string) (map[string]string, error) {
	var out map[string]string
	var err error

	switch task {
	case "add":
		_, err = s.rc.HSet(s.ctx, ActiveRoomsWithMetadataKey, roomId, metadata).Result()
		if err != nil {
			return nil, err
		}
	case "del":
		_, err = s.rc.HDel(s.ctx, ActiveRoomsWithMetadataKey, roomId).Result()
		if err != nil {
			return nil, err
		}
	case "get":
		result, err := s.rc.HGet(s.ctx, ActiveRoomsWithMetadataKey, roomId).Result()
		switch {
		case errors.Is(err, redis.Nil):
			return nil, nil
		case err != nil:
			return nil, err
		}
		out = map[string]string{
			roomId: result,
		}
	case "fetchAll":
		out, err = s.rc.HGetAll(s.ctx, ActiveRoomsWithMetadataKey).Result()
		if err != nil {
			return out, err
		}
	}

	return out, nil
}

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

// CheckAndWaitUntilRoomCreationInProgress will check the process & wait if needed
func (s *RedisService) CheckAndWaitUntilRoomCreationInProgress(roomId string) {
	for {
		list, err := s.RoomCreationProgressList(roomId, "exist")
		if err != nil {
			log.Errorln(err)
			break
		}
		if list {
			log.Println(roomId, "creation in progress, so waiting for", config.WaitDurationIfRoomInProgress)
			// we'll wait
			time.Sleep(config.WaitDurationIfRoomInProgress)
		} else {
			break
		}
	}
}
