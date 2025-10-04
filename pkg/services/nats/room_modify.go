package natsservice

import (
	"errors"
	"fmt"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go/jetstream"
)

// Constants for room info bucket and keys
const (
	RoomInfoBucketPrefix = Prefix + "roomInfo-"
	RoomInfoBucket       = RoomInfoBucketPrefix + "%s"

	RoomDbTableIdKey    = "id"
	RoomIdKey           = "room_id"
	RoomSidKey          = "room_sid"
	RoomEmptyTimeoutKey = "empty_timeout"
	RoomMaxParticipants = "max_participants"
	RoomStatusKey       = "status"
	RoomMetadataKey     = "metadata"
	RoomCreatedKey      = "created_at"

	RoomStatusCreated = "created"
	RoomStatusActive  = "active"
	RoomStatusEnded   = "ended"
)

// AddRoom creates a new room entry in the NATS JetStream Key-Value store
func (s *NatsService) AddRoom(tableId uint64, roomId, roomSid string, emptyTimeout, maxParticipants *uint32, metadata *plugnmeet.RoomMetadata) error {
	// Create or update the key-value bucket for the room
	bucket := fmt.Sprintf(RoomInfoBucket, roomId)
	kv, err := s.js.CreateOrUpdateKeyValue(s.ctx, jetstream.KeyValueConfig{
		Replicas: s.app.NatsInfo.NumReplicas,
		Bucket:   bucket,
	})
	if err != nil {
		return fmt.Errorf("failed to create or update KV bucket: %w", err)
	}

	// Set default values if not provided
	if emptyTimeout == nil {
		defaultTimeout := uint32(1800) // 30 minutes
		emptyTimeout = &defaultTimeout
	}
	if maxParticipants == nil {
		defaultMax := uint32(0) // 0 = unlimited
		maxParticipants = &defaultMax
	}

	// Marshal metadata to string
	mt, err := s.MarshalRoomMetadata(metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	// Prepare room data
	data := map[string]string{
		RoomDbTableIdKey:    fmt.Sprintf("%d", tableId),
		RoomIdKey:           roomId,
		RoomSidKey:          roomSid,
		RoomEmptyTimeoutKey: fmt.Sprintf("%d", *emptyTimeout),
		RoomMaxParticipants: fmt.Sprintf("%d", *maxParticipants),
		RoomStatusKey:       RoomStatusCreated,
		RoomCreatedKey:      fmt.Sprintf("%d", time.Now().UTC().Unix()),
		RoomMetadataKey:     mt,
	}

	// Store each key-value pair
	for k, v := range data {
		if _, err := kv.PutString(s.ctx, k, v); err != nil {
			return fmt.Errorf("failed to store room data for key %s: %w", k, err)
		}
	}
	// add room to watcher
	s.cs.AddRoomWatcher(kv, bucket, roomId)
	return nil
}

// updateRoomMetadata updates the metadata of an existing room
func (s *NatsService) updateRoomMetadata(roomId string, metadata interface{}) (string, error) {
	var mt *plugnmeet.RoomMetadata
	var err error
	// Handle different metadata input types
	switch v := metadata.(type) {
	case string:
		mt, err = s.UnmarshalRoomMetadata(v)
	case plugnmeet.RoomMetadata:
		mt = &v
	case *plugnmeet.RoomMetadata:
		mt = v
	default:
		return "", errors.New("invalid metadata data type")
	}
	if err != nil {
		return "", fmt.Errorf("failed to unmarshal metadata: %w", err)
	}

	// Retrieve the room's KV bucket
	bucket := fmt.Sprintf(RoomInfoBucket, roomId)
	kv, err := s.js.KeyValue(s.ctx, bucket)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		return "", fmt.Errorf("no room found with roomId: %s", roomId)
	} else if err != nil {
		return "", err
	}

	// Marshal and update metadata
	ml, err := s.MarshalRoomMetadata(mt)
	if err != nil {
		return "", fmt.Errorf("failed to marshal metadata: %w", err)
	}

	if _, err := kv.PutString(s.ctx, RoomMetadataKey, ml); err != nil {
		return "", fmt.Errorf("failed to update metadata: %w", err)
	}

	return ml, nil
}

// DeleteRoom removes the room's KV bucket
func (s *NatsService) DeleteRoom(roomId string) error {
	err := s.js.DeleteKeyValue(s.ctx, fmt.Sprintf(RoomInfoBucket, roomId))
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		return nil // Room already deleted
	}
	return err
}

// UpdateRoomStatus changes the status of a room
func (s *NatsService) UpdateRoomStatus(roomId string, status string) error {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf(RoomInfoBucket, roomId))
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		return fmt.Errorf("no room found with roomId: %s", roomId)
	} else if err != nil {
		return err
	}

	if _, err := kv.PutString(s.ctx, RoomStatusKey, status); err != nil {
		return fmt.Errorf("failed to update room status: %w", err)
	}

	return nil
}

// OnAfterSessionEndCleanup performs cleanup after a session ends
func (s *NatsService) OnAfterSessionEndCleanup(roomId string) {
	if err := s.DeleteRoom(roomId); err != nil {
		s.logger.WithError(err).Errorf("failed to delete room %s with error", roomId)
	}

	if err := s.DeleteAllRoomUsersWithConsumer(roomId); err != nil {
		s.logger.WithError(err).Errorf("failed to delete room %s users", roomId)
	}

	if err := s.DeleteRoomNatsStream(roomId); err != nil {
		s.logger.WithError(err).Errorf("failed to delete room %s", roomId)
	}

	if err := s.DeleteAllRoomFiles(roomId); err != nil {
		s.logger.WithError(err).Errorf("failed to delete room %s files bucket", roomId)
	}
}
