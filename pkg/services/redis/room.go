package redisservice

import (
	"errors"
	"fmt"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
)

const temporaryRoomData = Prefix + "temporaryRoomData:%s"

func (s *RedisService) HoldTemporaryRoomData(info *plugnmeet.NatsKvRoomInfo) {
	log := s.logger.WithFields(logrus.Fields{
		"roomId": info.RoomId,
		"sid":    info.RoomSid,
	})
	marshal, err := protojson.Marshal(info)
	if err != nil {
		log.WithError(err).Errorln("marshalling failed")
		return
	}
	key := fmt.Sprintf(temporaryRoomData, info.RoomId)

	err = s.rc.SetNX(s.ctx, key, marshal, time.Minute*1).Err()
	if err != nil {
		log.WithError(err).Errorln("SetNX failed")
	}
}

func (s *RedisService) GetTemporaryRoomData(roomId string) *plugnmeet.NatsKvRoomInfo {
	log := s.logger.WithFields(logrus.Fields{
		"roomId": roomId,
	})

	key := fmt.Sprintf(temporaryRoomData, roomId)
	val, err := s.rc.Get(s.ctx, key).Result()
	if err != nil {
		// It's normal for the key not to be found, so we don't log that as an error.
		// We only log actual Redis communication errors.
		if !errors.Is(err, redis.Nil) {
			log.WithError(err).Errorln("Get failed")
		}
		return nil
	}
	if val == "" {
		return nil
	}

	var info plugnmeet.NatsKvRoomInfo
	err = protojson.Unmarshal([]byte(val), &info)
	if err != nil {
		log.WithError(err).Errorln("unmarshalling failed")
		return nil
	}
	// otherwise there will be looping
	info.Status = natsservice.RoomStatusEnded
	return &info
}
