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
	RoomDbTableIdKey    = "id"
	RoomIdKey           = "room_id"
	RoomSidKey          = "room_sid"
	RoomEmptyTimeoutKey = "empty_timeout"
	RoomMaxParticipants = "max_participants"
	RoomStatusKey       = "status"
	RoomMetadataKey     = "metadata"
	RoomCreatedKey      = "created_at"

	RoomStatusCreated      = "created"
	RoomStatusActive       = "active"
	RoomStatusTriggeredEnd = "triggered_end"
	RoomStatusEnded        = "ended"
)

// AddRoom creates a new room entry in the NATS JetStream Key-Value store
func (s *NatsService) AddRoom(tableId uint64, roomId, roomSid string, emptyTimeout, maxParticipants *uint32, metadata *plugnmeet.RoomMetadata) (string, error) {
	// Create or update the key-value bucket for the room
	bucket := s.formatConsolidatedRoomBucket(roomId)
	roomTitle := metadata.GetRoomTitle()
	if len(roomTitle) > 15 {
		roomTitle = roomTitle[:15]
	}
	kv, err := s.js.CreateOrUpdateKeyValue(s.ctx, jetstream.KeyValueConfig{
		Replicas:    s.app.NatsInfo.NumReplicas,
		Bucket:      bucket,
		TTL:         DefaultTTL,
		Description: roomTitle,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create or update KV bucket: %w", err)
	}

	// CRITICAL: Add room to watcher here to initialize cache maps BEFORE updating them.
	s.cs.addRoomWatcher(kv, bucket, roomId)

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
		return "", fmt.Errorf("failed to marshal metadata: %w", err)
	}

	// Prepare room data using short field names as keys
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

	// Store each key-value pair and update cache manually
	for field, v := range data {
		key := s.formatRoomKey(field)
		rev, err := kv.PutString(s.ctx, key, v)
		if err != nil {
			return "", fmt.Errorf("failed to store room data for key %s: %w", field, err)
		}
		s.cs.setRoomInfoCache(roomId, field, v, rev)
	}

	return mt, nil
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
	kv, err := s.js.KeyValue(s.ctx, s.formatConsolidatedRoomBucket(roomId))
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

	rev, err := kv.PutString(s.ctx, s.formatRoomKey(RoomMetadataKey), ml)
	if err != nil {
		return "", fmt.Errorf("failed to update metadata: %w", err)
	}
	s.cs.setRoomInfoCache(roomId, RoomMetadataKey, ml, rev)

	return ml, nil
}

// DeleteRoom removes the room's KV bucket
func (s *NatsService) DeleteRoom(roomId string) error {
	err := s.js.DeleteKeyValue(s.ctx, s.formatConsolidatedRoomBucket(roomId))
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		return nil // Room already deleted
	}
	return err
}

// UpdateRoomStatus changes the status of a room
func (s *NatsService) UpdateRoomStatus(roomId string, status string) error {
	kv, err := s.js.KeyValue(s.ctx, s.formatConsolidatedRoomBucket(roomId))
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		return fmt.Errorf("no room found with roomId: %s", roomId)
	} else if err != nil {
		return err
	}

	rev, err := kv.PutString(s.ctx, s.formatRoomKey(RoomStatusKey), status)
	if err != nil {
		return fmt.Errorf("failed to update room status: %w", err)
	}

	// Manually update the cache to avoid watch latency
	s.cs.setRoomInfoCache(roomId, RoomStatusKey, status, rev)

	return nil
}

// OnAfterSessionEndCleanup is the final, authoritative cleanup process for a room in NATS.
// It ensures all consumers are deleted, all messages are purged, and the room's KV bucket is removed.
// This function acts as a safety net to prevent orphaned NATS resources.
func (s *NatsService) OnAfterSessionEndCleanup(roomId string) {
	// silently delete everything without log
	_ = s.deleteAllUserConsumers(roomId)
	_ = s.PurgeRoomMessagesFromStream(roomId)
	_ = s.DeleteRoom(roomId)
}
