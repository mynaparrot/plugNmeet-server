package redisservice

import (
	"encoding/json"
	"fmt"
	"time"
)

const transcriptionHistoryPrefix = Prefix + "transcription_history:" // A HASH for each room

type TranscriptionChunk struct {
	FromUserID string `json:"from_user_id"`
	Name       string `json:"name"`
	Lang       string `json:"lang"`
	Text       string `json:"text"`
}

// formatTranscriptionHistoryKey generates the Redis key for the transcription history of a room.
func (s *RedisService) formatTranscriptionHistoryKey(roomId string) string {
	return fmt.Sprintf("%s%s", transcriptionHistoryPrefix, roomId)
}

// AddTranscriptionToHistory adds a new transcription chunk to the room's history in Redis.
func (s *RedisService) AddTranscriptionToHistory(roomId, userId, name, lang, text string) error {
	key := s.formatTranscriptionHistoryKey(roomId)
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

	// The field will be the timestamp
	field := fmt.Sprintf("%d", time.Now().UnixNano())

	pipe := s.rc.Pipeline()
	pipe.HSet(s.ctx, key, field, jsonData)
	pipe.Expire(s.ctx, key, DefaultTTL)

	_, err = pipe.Exec(s.ctx)
	return err
}

// GetTranscriptionHistory retrieves all transcription chunks for a given room from Redis.
func (s *RedisService) GetTranscriptionHistory(roomId string) (map[string]string, error) {
	key := s.formatTranscriptionHistoryKey(roomId)
	result, err := s.rc.HGetAll(s.ctx, key).Result()
	if err != nil {
		return nil, err
	}

	if len(result) == 0 {
		return nil, nil
	}

	return result, nil
}

// DeleteTranscriptionHistory deletes the entire transcription history for a room from Redis.
func (s *RedisService) DeleteTranscriptionHistory(roomId string) {
	key := s.formatTranscriptionHistoryKey(roomId)
	// We don't need to check the error for a cleanup operation.
	_ = s.rc.Del(s.ctx, key).Err()
}
