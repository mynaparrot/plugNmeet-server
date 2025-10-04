package natsservice

import (
	"errors"
	"fmt"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/encoding/protojson"
)

// Constants for room files bucket and keys
const (
	RoomFilesBucketPrefix = Prefix + "roomFiles-"
	RoomFilesBucket       = RoomFilesBucketPrefix + "%s"
)

// AddRoomFile adds or updates a file's metadata in the room's file bucket.
// The fileId will be used as the key.
func (s *NatsService) AddRoomFile(roomId string, meta *plugnmeet.RoomUploadedFileMetadata) error {
	kv, err := s.js.CreateOrUpdateKeyValue(s.ctx, jetstream.KeyValueConfig{
		Replicas: s.app.NatsInfo.NumReplicas,
		Bucket:   fmt.Sprintf(RoomFilesBucket, roomId),
	})
	if err != nil {
		return err
	}

	metaBytes, err := protojson.Marshal(meta)
	if err != nil {
		return fmt.Errorf("failed to marshal file metadata: %w", err)
	}

	_, err = kv.Put(s.ctx, meta.FileId, metaBytes)
	if err != nil {
		return err
	}
	return nil
}

// DeleteRoomFile removes a file's metadata from the room's file bucket.
func (s *NatsService) DeleteRoomFile(roomId, fileId string) error {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf(RoomFilesBucket, roomId))
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		return nil
	case err != nil:
		return err
	}

	return kv.Purge(s.ctx, fileId)
}

// GetRoomFile retrieves a specific file's metadata.
func (s *NatsService) GetRoomFile(roomId, fileId string) (*plugnmeet.RoomUploadedFileMetadata, error) {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf(RoomFilesBucket, roomId))
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		return nil, nil
	case err != nil:
		return nil, err
	}

	entry, err := kv.Get(s.ctx, fileId)
	switch {
	case errors.Is(err, jetstream.ErrKeyNotFound):
		return nil, nil
	case err != nil:
		return nil, err
	}

	meta := new(plugnmeet.RoomUploadedFileMetadata)
	err = protojson.Unmarshal(entry.Value(), meta)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal file metadata: %w", err)
	}

	return meta, nil
}

// GetAllRoomFiles retrieves all file metadata for a given room.
func (s *NatsService) GetAllRoomFiles(roomId string) (map[string]*plugnmeet.RoomUploadedFileMetadata, error) {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf(RoomFilesBucket, roomId))
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

	files := make(map[string]*plugnmeet.RoomUploadedFileMetadata)
	for k := range keys.Keys() {
		if entry, err := kv.Get(s.ctx, k); err == nil && entry != nil {
			meta := new(plugnmeet.RoomUploadedFileMetadata)
			err = protojson.Unmarshal(entry.Value(), meta)
			if err == nil {
				files[k] = meta
			}
		}
	}

	return files, nil
}

// DeleteAllRoomFiles purges the entire file bucket for a room, typically on session end.
func (s *NatsService) DeleteAllRoomFiles(roomId string) error {
	return s.js.DeleteKeyValue(s.ctx, fmt.Sprintf(RoomFilesBucket, roomId))
}
