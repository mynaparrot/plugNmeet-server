package redisservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// AiAgentQuizMetadataTTL bounds how long a quiz answer key outlives its poll.
	AiAgentQuizMetadataTTL = 24 * time.Hour
	aiAgentProgressTTL     = 24 * time.Hour
)

// AiAgentKnowledgeCacheKey is the key of one fetched knowledge document. The
// hash keeps the URL out of the key while staying stable per page.
func AiAgentKnowledgeCacheKey(roomId, rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return fmt.Sprintf("%s:%s:kb:%s", AiAgentRedisKey, roomId, hex.EncodeToString(sum[:]))
}

func aiAgentQuizKey(roomId, pollId string) string {
	return fmt.Sprintf("%s:%s:quiz:%s", AiAgentRedisKey, roomId, pollId)
}

func aiAgentProgressKey(roomId string) string {
	return fmt.Sprintf("%s:%s:progress", AiAgentRedisKey, roomId)
}

func aiAgentTelemetryKey(roomId string) string {
	return fmt.Sprintf("%s:%s:telemetry", AiAgentRedisKey, roomId)
}

// AIQuizMetadata is the answer key of a quiz poll.
type AIQuizMetadata struct {
	CorrectOptionIndex int    `json:"correct_option_index"`
	Explanation        string `json:"explanation,omitempty"`
}

// AIProgressEntry is one concept the participant worked on.
type AIProgressEntry struct {
	Concept string `json:"concept"`
	Status  string `json:"status"`
	Notes   string `json:"notes,omitempty"`
}

// AISessionTranscriptTurn is one spoken turn kept for the session summary.
type AISessionTranscriptTurn struct {
	Role string `json:"role"` // "participant" or "agent"
	Text string `json:"text"`
}

// AIAgentSessionTelemetry is the session's counters and transcript, flushed
// while the agent runs so an abrupt crash still leaves usable metadata.
type AIAgentSessionTelemetry struct {
	SessionID              string                    `json:"session_id"`
	AgentName              string                    `json:"agent_name"`
	Topic                  string                    `json:"topic"`
	AllowedSubtopics       []string                  `json:"allowed_subtopics,omitempty"`
	StartedAt              int64                     `json:"started_at"`
	EndedAt                int64                     `json:"ended_at"`
	EndReason              string                    `json:"end_reason,omitempty"`
	TurnsCount             int                       `json:"turns_count"`
	OffTopicRedirectsCount int                       `json:"off_topic_redirects_count"`
	InputTokens            uint64                    `json:"input_tokens"`
	OutputTokens           uint64                    `json:"output_tokens"`
	AudioMillis            uint64                    `json:"audio_millis"`
	ToolsUsed              map[string]int            `json:"tools_used,omitempty"`
	Transcript             []AISessionTranscriptTurn `json:"transcript,omitempty"`
}

// GetAIKnowledgeDocument returns the cached document of rawURL, if any.
func (s *RedisService) GetAIKnowledgeDocument(ctx context.Context, roomId, rawURL string) (string, bool, error) {
	value, err := s.rc.Get(ctx, AiAgentKnowledgeCacheKey(roomId, rawURL)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", false, nil
		}
		return "", false, err
	}
	return value, true, nil
}

// SetAIKnowledgeDocument caches one knowledge document.
func (s *RedisService) SetAIKnowledgeDocument(ctx context.Context, roomId, rawURL, value string, ttl time.Duration) error {
	return s.rc.Set(ctx, AiAgentKnowledgeCacheKey(roomId, rawURL), value, ttl).Err()
}

// SaveAIQuizMetadata stores the answer key of a quiz next to its poll.
func (s *RedisService) SaveAIQuizMetadata(ctx context.Context, roomId, pollId string, correctOptionIndex int, explanation string) error {
	encoded, err := json.Marshal(AIQuizMetadata{CorrectOptionIndex: correctOptionIndex, Explanation: explanation})
	if err != nil {
		return err
	}
	return s.rc.Set(ctx, aiAgentQuizKey(roomId, pollId), encoded, AiAgentQuizMetadataTTL).Err()
}

// GetAIQuizMetadata returns the stored answer key; found is false when the poll
// is not a quiz.
func (s *RedisService) GetAIQuizMetadata(ctx context.Context, roomId, pollId string) (int, string, bool, error) {
	encoded, err := s.rc.Get(ctx, aiAgentQuizKey(roomId, pollId)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return 0, "", false, nil
		}
		return 0, "", false, err
	}
	meta := AIQuizMetadata{}
	if err := json.Unmarshal(encoded, &meta); err != nil {
		return 0, "", false, err
	}
	return meta.CorrectOptionIndex, meta.Explanation, true, nil
}

// RecordAILearningProgress upserts one concept of the room's progress hash.
func (s *RedisService) RecordAILearningProgress(ctx context.Context, roomId string, entry AIProgressEntry) error {
	encoded, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	key := aiAgentProgressKey(roomId)
	pipe := s.rc.TxPipeline()
	pipe.HSet(ctx, key, entry.Concept, encoded)
	pipe.Expire(ctx, key, aiAgentProgressTTL)
	_, err = pipe.Exec(ctx)
	return err
}

// GetAILearningProgress returns the room's progress entries keyed by concept.
func (s *RedisService) GetAILearningProgress(ctx context.Context, roomId string) (map[string]AIProgressEntry, error) {
	raw, err := s.rc.HGetAll(ctx, aiAgentProgressKey(roomId)).Result()
	if err != nil {
		return nil, err
	}
	entries := make(map[string]AIProgressEntry, len(raw))
	for concept, encoded := range raw {
		entry := AIProgressEntry{}
		if json.Unmarshal([]byte(encoded), &entry) != nil {
			continue
		}
		entries[concept] = entry
	}
	return entries, nil
}

// SaveAIAgentSessionTelemetry stores the room's session telemetry.
func (s *RedisService) SaveAIAgentSessionTelemetry(ctx context.Context, roomId string, t AIAgentSessionTelemetry) error {
	encoded, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return s.rc.Set(ctx, aiAgentTelemetryKey(roomId), encoded, aiAgentProgressTTL).Err()
}

// GetAIAgentSessionTelemetry returns the room's telemetry; a room without a
// session reports nil, nil.
func (s *RedisService) GetAIAgentSessionTelemetry(ctx context.Context, roomId string) (*AIAgentSessionTelemetry, error) {
	encoded, err := s.rc.Get(ctx, aiAgentTelemetryKey(roomId)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	telemetry := &AIAgentSessionTelemetry{}
	if err := json.Unmarshal(encoded, telemetry); err != nil {
		return nil, err
	}
	return telemetry, nil
}

// DeleteAIAgentSessionData drops what one session left behind; the summary that
// follows reads it before calling this.
func (s *RedisService) DeleteAIAgentSessionData(ctx context.Context, roomId string) {
	if err := s.rc.Del(ctx, aiAgentTelemetryKey(roomId), aiAgentProgressKey(roomId)).Err(); err != nil {
		s.logger.WithError(err).Debug("failed to delete the ai agent session data")
	}
}
