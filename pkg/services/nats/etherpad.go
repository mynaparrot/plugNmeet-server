package natsservice

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

const (
	EtherpadBucket         = Prefix + "etherpad"
	EtherpadRoomKeyPrefix  = "room_"
	EtherpadTokenKeyPrefix = "token_"
	EtherpadRoomIdPrefix   = "-ROOMID_"
)

// formatEtherpadRoomKey generates the key for an etherpad room entry.
func formatEtherpadRoomKey(nodeId, roomId string) string {
	return fmt.Sprintf("%s%s%s%s", EtherpadRoomKeyPrefix, nodeId, EtherpadRoomIdPrefix, roomId)
}

// formatEtherpadTokenKey generates the key for an etherpad token entry.
func formatEtherpadTokenKey(nodeId string) string {
	return fmt.Sprintf("%s%s", EtherpadTokenKeyPrefix, nodeId)
}

// ensureEtherpadBucket creates the consolidated etherpad bucket if it doesn't exist.
func (s *NatsService) ensureEtherpadBucket() (jetstream.KeyValue, error) {
	kv, err := s.js.CreateOrUpdateKeyValue(s.ctx, jetstream.KeyValueConfig{
		Replicas:       s.app.NatsInfo.NumReplicas,
		Bucket:         EtherpadBucket,
		LimitMarkerTTL: time.Second,
	})
	if err != nil {
		return nil, err
	}
	return kv, nil
}

// AddRoomInEtherpad records that a room is active on a specific etherpad node.
func (s *NatsService) AddRoomInEtherpad(nodeId, roomId string) error {
	kv, err := s.ensureEtherpadBucket()
	if err != nil {
		return fmt.Errorf("failed to get etherpad bucket: %w", err)
	}

	key := formatEtherpadRoomKey(nodeId, roomId)
	if _, err = kv.Create(s.ctx, key, []byte(fmt.Sprintf("%d", time.Now().UnixMilli())), jetstream.KeyTTL(DefaultTTL)); err != nil {
		if errors.Is(err, jetstream.ErrKeyExists) {
			return nil
		}
		return err
	}
	return nil
}

// GetEtherpadActiveRoomsNum counts how many rooms are active on a specific etherpad node.
func (s *NatsService) GetEtherpadActiveRoomsNum(nodeId string) (int64, error) {
	kv, err := s.js.KeyValue(s.ctx, EtherpadBucket)
	if err != nil {
		if errors.Is(err, jetstream.ErrBucketNotFound) {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to get etherpad bucket: %w", err)
	}

	keys, err := kv.ListKeys(s.ctx)
	if err != nil {
		return 0, err
	}

	var count int64
	prefix := fmt.Sprintf("%s%s%s", EtherpadRoomKeyPrefix, nodeId, EtherpadRoomIdPrefix)
	for key := range keys.Keys() {
		if strings.HasPrefix(key, prefix) {
			count++
		}
	}
	return count, nil
}

// RemoveRoomFromEtherpad removes the record of a room being active on a node.
func (s *NatsService) RemoveRoomFromEtherpad(nodeId, roomId string) error {
	kv, err := s.js.KeyValue(s.ctx, EtherpadBucket)
	if err != nil {
		if errors.Is(err, jetstream.ErrBucketNotFound) {
			return nil
		}
		return fmt.Errorf("failed to get etherpad bucket: %w", err)
	}

	key := formatEtherpadRoomKey(nodeId, roomId)
	err = kv.Purge(s.ctx, key)
	if err != nil && !errors.Is(err, jetstream.ErrKeyNotFound) {
		// We still check for ErrKeyNotFound in case the key was already purged or expired.
		return err
	}
	return nil
}

// AddEtherpadToken stores a temporary access token with a specific TTL on the key.
func (s *NatsService) AddEtherpadToken(nodeId, token string, expiration time.Duration) error {
	kv, err := s.ensureEtherpadBucket()
	if err != nil {
		return fmt.Errorf("failed to get etherpad bucket: %w", err)
	}

	key := formatEtherpadTokenKey(nodeId)
	if _, err = kv.Create(s.ctx, key, []byte(token), jetstream.KeyTTL(expiration)); err != nil {
		if errors.Is(err, jetstream.ErrKeyExists) {
			return nil
		}
		return err
	}

	return nil
}

// GetEtherpadToken retrieves a temporary access token.
func (s *NatsService) GetEtherpadToken(nodeId string) (string, error) {
	kv, err := s.js.KeyValue(s.ctx, EtherpadBucket)
	if err != nil {
		if errors.Is(err, jetstream.ErrBucketNotFound) {
			return "", nil
		}
		return "", fmt.Errorf("failed to get etherpad bucket: %w", err)
	}

	key := formatEtherpadTokenKey(nodeId)
	entry, err := kv.Get(s.ctx, key)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return "", nil
		}
		return "", err
	}

	return string(entry.Value()), nil
}
