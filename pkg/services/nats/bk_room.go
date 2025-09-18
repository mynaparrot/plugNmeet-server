package natsservice

import (
	"errors"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"
)

const breakoutRoomBucket = Prefix + "breakoutRoom-%s"

func (s *NatsService) InsertOrUpdateBreakoutRoom(parentRoomId, bkRoomId string, val []byte) error {
	kv, err := s.js.CreateOrUpdateKeyValue(s.ctx, jetstream.KeyValueConfig{
		Replicas: s.app.NatsInfo.NumReplicas,
		Bucket:   fmt.Sprintf(breakoutRoomBucket, parentRoomId),
	})
	if err != nil {
		return err
	}

	_, err = kv.Put(s.ctx, bkRoomId, val)
	if err != nil {
		return err
	}
	return nil
}

func (s *NatsService) DeleteBreakoutRoom(parentRoomId, bkRoomId string) error {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf(breakoutRoomBucket, parentRoomId))
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		return nil
	case err != nil:
		return err
	}

	return kv.Purge(s.ctx, bkRoomId)
}

func (s *NatsService) GetBreakoutRoom(parentRoomId, bkRoomId string) ([]byte, error) {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf(breakoutRoomBucket, parentRoomId))
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		return nil, nil
	case err != nil:
		return nil, err
	}

	entry, err := kv.Get(s.ctx, bkRoomId)
	switch {
	case errors.Is(err, jetstream.ErrKeyNotFound):
		return nil, nil
	case err != nil:
		return nil, err
	}

	if entry == nil {
		return nil, nil
	}

	return entry.Value(), nil
}

func (s *NatsService) CountBreakoutRooms(parentRoomId string) (int64, error) {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf(breakoutRoomBucket, parentRoomId))
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

func (s *NatsService) GetAllBreakoutRoomsByParentRoomId(parentRoomId string) (map[string][]byte, error) {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf(breakoutRoomBucket, parentRoomId))
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		return nil, nil
	case err != nil:
		return nil, err
	}

	keys, err := kv.ListKeys(s.ctx)
	if err != nil {
		return nil, err
	}

	rooms := make(map[string][]byte)
	for k := range keys.Keys() {
		if et, err := kv.Get(s.ctx, k); err == nil && et != nil {
			rooms[k] = et.Value()
		}
	}

	return rooms, nil
}

func (s *NatsService) DeleteAllBreakoutRoomsByParentRoomId(parentRoomId string) {
	_ = s.js.DeleteKeyValue(s.ctx, fmt.Sprintf(breakoutRoomBucket, parentRoomId))
}

func (s *NatsService) GetBreakoutRoomIdsByParentRoomId(parentRoomId string) ([]string, error) {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf(breakoutRoomBucket, parentRoomId))
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		return nil, nil
	case err != nil:
		return nil, err
	}

	keys, err := kv.ListKeys(s.ctx)
	if err != nil {
		return nil, err
	}

	var ids []string
	for k := range keys.Keys() {
		ids = append(ids, k)
	}

	return ids, nil
}
