package redisservice

import (
	"github.com/goccy/go-json"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/redis/go-redis/v9"
)

func (s *RedisService) PublishToWebsocketChannel(channel string, msg interface{}) error {
	_, err := s.rc.Publish(s.ctx, channel, msg).Result()
	if err != nil {
		return err
	}
	return nil
}

func (s *RedisService) SubscribeToWebsocketChannel(channel string) (*redis.PubSub, error) {
	pubSub := s.rc.Subscribe(s.ctx, channel)
	_, err := pubSub.Receive(s.ctx)
	if err != nil {
		return nil, err
	}
	return pubSub, nil
}

type websocketToRedis struct {
	Type    string                 `json:"type,omitempty"`
	DataMsg *plugnmeet.DataMessage `json:"data_msg,omitempty"`
	RoomId  string                 `json:"room_id,omitempty"`
	IsAdmin bool                   `json:"is_admin,omitempty"`
}

func (s *RedisService) DistributeWebsocketMsgToRedisChannel(payload *websocketToRedis) error {
	msg, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	switch payload.DataMsg.Type {
	case plugnmeet.DataMsgType_USER:
		err = s.PublishToWebsocketChannel(config.UserWebsocketChannel, msg)
		if err != nil {
			return err
		}
	case plugnmeet.DataMsgType_WHITEBOARD:
		err = s.PublishToWebsocketChannel(config.WhiteboardWebsocketChannel, msg)
		if err != nil {
			return err
		}
	case plugnmeet.DataMsgType_SYSTEM:
		err = s.PublishToWebsocketChannel(config.SystemWebsocketChannel, msg)
		if err != nil {
			return err
		}
	}

	return nil
}
