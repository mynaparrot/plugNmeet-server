package natsservice

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/encoding/protojson"
)

// AddRoomFile adds or updates a file's metadata in the consolidated room bucket.
func (s *NatsService) AddRoomFile(roomId string, meta *plugnmeet.RoomUploadedFileMetadata) error {
	kv, err := s.js.KeyValue(s.ctx, s.formatConsolidatedRoomBucket(roomId))
	if err != nil {
		return fmt.Errorf("could not get consolidated room bucket: %w", err)
	}

	metaBytes, err := protojson.Marshal(meta)
	if err != nil {
		return fmt.Errorf("failed to marshal file metadata: %w", err)
	}

	_, err = kv.Put(s.ctx, s.formatFileKey(meta.FileId), metaBytes)
	if err != nil {
		return err
	}
	return nil
}

// DeleteRoomFile removes a file's metadata from the consolidated room bucket.
func (s *NatsService) DeleteRoomFile(roomId, fileId string) error {
	kv, err := s.js.KeyValue(s.ctx, s.formatConsolidatedRoomBucket(roomId))
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		return nil
	case err != nil:
		return err
	}

	return kv.Purge(s.ctx, s.formatFileKey(fileId))
}

// GetRoomFile retrieves a specific file's metadata.
// It first checks the local cache, and falls back to a direct NATS KV lookup on a cache miss.
func (s *NatsService) GetRoomFile(roomId, fileId string) (*plugnmeet.RoomUploadedFileMetadata, error) {
	// Try to get from cache first for high performance.
	if file, ok := s.cs.getCachedRoomFile(roomId, fileId); ok && file != nil {
		return file, nil
	}

	// Fallback: If not in cache, fetch directly from NATS KV.
	kv, err := s.js.KeyValue(s.ctx, s.formatConsolidatedRoomBucket(roomId))
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		return nil, nil
	case err != nil:
		return nil, err
	}

	entry, err := kv.Get(s.ctx, s.formatFileKey(fileId))
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
// It first checks the local cache, and falls back to a direct NATS KV lookup on a cache miss.
func (s *NatsService) GetAllRoomFiles(roomId string) (map[string]*plugnmeet.RoomUploadedFileMetadata, error) {
	// Try to get from cache first for high performance.
	if files, ok := s.cs.getAllCachedRoomFiles(roomId); ok && len(files) > 0 {
		return files, nil
	}

	// Fallback: If not in cache, fetch directly from NATS KV.
	kv, err := s.js.KeyValue(s.ctx, s.formatConsolidatedRoomBucket(roomId))
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
	for key := range keys.Keys() {
		if strings.HasPrefix(key, FileKeyPrefix) {
			if entry, err := kv.Get(s.ctx, key); err == nil && entry != nil {
				meta := new(plugnmeet.RoomUploadedFileMetadata)
				err = protojson.Unmarshal(entry.Value(), meta)
				if err == nil {
					files[meta.FileId] = meta
				}
			}
		}
	}

	return files, nil
}
