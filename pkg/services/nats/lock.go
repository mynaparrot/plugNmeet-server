package natsservice

import (
	"fmt"
	"github.com/nats-io/nats.go/jetstream"
	"time"
)

const (
	RoomCreationLockBucket = Prefix + "roomCreationLock-%s"
	SchedulerLockBucket    = Prefix + "schedulerLock-%s"
)

func (s *NatsService) LockRoomCreation(roomId string, ttl time.Duration) error {
	_, err := s.js.CreateOrUpdateKeyValue(s.ctx, jetstream.KeyValueConfig{
		Bucket: fmt.Sprintf(RoomCreationLockBucket, roomId),
		TTL:    ttl,
	})
	if err != nil {
		return err
	}
	return nil
}

func (s *NatsService) IsRoomCreationLock(roomId string) bool {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf(RoomCreationLockBucket, roomId))
	if err != nil {
		return false
	}
	if kv != nil {
		return true
	}
	return false
}

func (s *NatsService) UnlockRoomCreation(roomId string) {
	_ = s.js.DeleteKeyValue(s.ctx, fmt.Sprintf(RoomCreationLockBucket, roomId))
}

func (s *NatsService) LockSchedulerTask(task string, ttl time.Duration) error {
	_, err := s.js.CreateOrUpdateKeyValue(s.ctx, jetstream.KeyValueConfig{
		Bucket: fmt.Sprintf(SchedulerLockBucket, task),
		TTL:    ttl,
	})
	if err != nil {
		return err
	}
	return nil
}

func (s *NatsService) IsSchedulerTaskLock(task string) bool {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf(SchedulerLockBucket, task))
	if err != nil {
		return false
	}
	if kv != nil {
		return true
	}
	return false
}

func (s *NatsService) UnlockSchedulerTask(task string) {
	_ = s.js.DeleteKeyValue(s.ctx, fmt.Sprintf(SchedulerLockBucket, task))
}
