package natsservice

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

const transcriptionBucket = Prefix + "transcription_chunks-%s"

type TranscriptionChunk struct {
	FromUserID string `json:"from_user_id"`
	Name       string `json:"name"`
	Lang       string `json:"lang"`
	Text       string `json:"text"`
}

// AddTranscriptionChunk adds a new transcription chunk to the room's KV bucket.
func (s *NatsService) AddTranscriptionChunk(roomId, userId, name, lang, text string) error {
	kv, err := s.js.CreateOrUpdateKeyValue(s.ctx, jetstream.KeyValueConfig{
		Replicas: s.app.NatsInfo.NumReplicas,
		Bucket:   fmt.Sprintf(transcriptionBucket, roomId),
		TTL:      DefaultTTL,
	})
	if err != nil {
		return err
	}

	chunk := TranscriptionChunk{
		FromUserID: userId,
		Name:       name,
		Lang:       lang,
		Text:       text,
	}
	jsonData, err := json.Marshal(chunk)
	if err != nil {
		return err
	}

	key := fmt.Sprintf("%d", time.Now().UnixNano())
	_, err = kv.Put(s.ctx, key, jsonData)
	return err
}

// GetTranscriptionChunks retrieves all transcription chunks for a given room.
// It returns a map where the key is the NATS key ({userId}:{timestamp}) and the value is the JSON data.
func (s *NatsService) GetTranscriptionChunks(roomId string) (map[string][]byte, error) {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf(transcriptionBucket, roomId))
	if err != nil {
		if errors.Is(err, jetstream.ErrBucketNotFound) {
			return nil, nil
		}
		return nil, err
	}

	keys, err := kv.ListKeys(s.ctx)
	if err != nil {
		return nil, err
	}

	chunks := make(map[string][]byte)
	for k := range keys.Keys() {
		if entry, err := kv.Get(s.ctx, k); err == nil && entry != nil {
			chunks[k] = entry.Value()
		}
	}

	return chunks, nil
}

// DeleteTranscriptionBucket deletes the entire KV bucket for a room's transcription.
func (s *NatsService) DeleteTranscriptionBucket(roomId string) {
	_ = s.js.DeleteKeyValue(s.ctx, fmt.Sprintf(transcriptionBucket, roomId))
}
