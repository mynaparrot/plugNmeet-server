package redisservice

import (
	"fmt"
	"time"
)

const (
	EtherpadKey      = "pnm:etherpad:"
	EtherpadTokenKey = "pnm:etherpadToken"
)

func (s *RedisService) AddRoomInEtherpad(nodeId, roomId string) error {
	_, err := s.rc.SAdd(s.ctx, EtherpadKey+nodeId, roomId).Result()
	if err != nil {
		return err
	}
	return nil
}

func (s *RedisService) GetEtherpadActiveRoomsNum(nodeId string) (int64, error) {
	c, err := s.rc.SCard(s.ctx, EtherpadKey+nodeId).Result()
	if err != nil {
		return 0, err
	}
	return c, nil
}

func (s *RedisService) RemoveRoomFromEtherpad(nodeId, roomId string) error {
	_, err := s.rc.SRem(s.ctx, EtherpadKey+nodeId, roomId).Result()
	if err != nil {
		return err
	}
	return nil
}

func (s *RedisService) AddEtherpadToken(nodeId, token string, expiration time.Duration) error {
	key := fmt.Sprintf("%s:%s", EtherpadTokenKey, nodeId)
	_, err := s.rc.Set(s.ctx, key, token, expiration).Result()
	if err != nil {
		return err
	}
	return nil
}

func (s *RedisService) GetEtherpadToken(nodeId string) (string, error) {
	key := fmt.Sprintf("%s:%s", EtherpadTokenKey, nodeId)
	token, err := s.rc.Get(s.ctx, key).Result()
	if err != nil {
		return "", err
	}

	return token, nil
}
