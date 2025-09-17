package natsservice

import (
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

const (
	EtherpadKvKey      = Prefix + "etherpad-%s"
	EtherpadTokenKvKey = Prefix + "etherpadToken-%s"
)

func (s *NatsService) AddRoomInEtherpad(nodeId, roomId string) error {
	kv, err := s.js.CreateOrUpdateKeyValue(s.ctx, jetstream.KeyValueConfig{
		Replicas: s.app.NatsInfo.NumReplicas,
		Bucket:   fmt.Sprintf(EtherpadKvKey, nodeId),
	})
	if err != nil {
		return err
	}
	_, err = kv.PutString(s.ctx, roomId, fmt.Sprintf("%d", time.Now().UnixMilli()))
	if err != nil {
		return err
	}
	return nil
}

func (s *NatsService) GetEtherpadActiveRoomsNum(nodeId string) (int64, error) {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf(EtherpadKvKey, nodeId))
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		return 0, nil
	case err != nil:
		return 0, err
	}

	keys, err := kv.ListKeys(s.ctx)
	if err != nil {
		return 0, err
	}

	var count int64
	for range keys.Keys() {
		count++
	}

	return count, nil
}

func (s *NatsService) RemoveRoomFromEtherpad(nodeId, roomId string) error {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf(EtherpadKvKey, nodeId))
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		return nil
	case err != nil:
		return err
	}
	err = kv.Purge(s.ctx, roomId)
	if err != nil {
		return err
	}
	return nil
}

func (s *NatsService) AddEtherpadToken(nodeId, token string, expiration time.Duration) error {
	kv, err := s.js.CreateOrUpdateKeyValue(s.ctx, jetstream.KeyValueConfig{
		Replicas: s.app.NatsInfo.NumReplicas,
		Bucket:   fmt.Sprintf(EtherpadTokenKvKey, nodeId),
		TTL:      expiration,
	})
	if err != nil {
		return err
	}
	_, err = kv.PutString(s.ctx, nodeId, token)
	if err != nil {
		return err
	}
	return nil
}

func (s *NatsService) GetEtherpadToken(nodeId string) (string, error) {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf(EtherpadTokenKvKey, nodeId))
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		return "", nil
	case err != nil:
		return "", err
	}

	entry, err := kv.Get(s.ctx, nodeId)
	switch {
	case errors.Is(err, jetstream.ErrKeyNotFound):
		return "", nil
	case err != nil:
		return "", err
	}

	return string(entry.Value()), nil
}
