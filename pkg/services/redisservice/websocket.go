package redisservice

import (
	"github.com/goccy/go-json"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
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

type WebsocketToRedis struct {
	Type    string                 `json:"type,omitempty"`
	DataMsg *plugnmeet.DataMessage `json:"data_msg,omitempty"`
	RoomId  string                 `json:"room_id,omitempty"`
	IsAdmin bool                   `json:"is_admin,omitempty"`
}

func (s *RedisService) DistributeWebsocketMsgToRedisChannel(payload *WebsocketToRedis) {
	msg, err := json.Marshal(payload)
	if err != nil {
		log.Errorln(err)
		return
	}

	switch payload.DataMsg.Type {
	case plugnmeet.DataMsgType_USER:
		_ = s.PublishToWebsocketChannel(config.UserWebsocketChannel, msg)
	case plugnmeet.DataMsgType_WHITEBOARD:
		_ = s.PublishToWebsocketChannel(config.WhiteboardWebsocketChannel, msg)
	case plugnmeet.DataMsgType_SYSTEM:
		_ = s.PublishToWebsocketChannel(config.SystemWebsocketChannel, msg)
	}
}
