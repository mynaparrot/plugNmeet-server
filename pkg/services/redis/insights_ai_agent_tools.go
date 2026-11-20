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
	SessionID string `json:"session_id"`
	// Learner identity of the 1-on-1 session, so the summary artifact can still
	// name the participant after they left the room.
	UserID                 string   `json:"user_id,omitempty"`
	UserName               string   `json:"user_name,omitempty"`
	ExUserID               string   `json:"ex_user_id,omitempty"`
	ExtraData              string   `json:"extra_data,omitempty"`
	AgentName              string   `json:"agent_name"`
	Topic                  string   `json:"topic"`
	AllowedSubtopics       []string `json:"allowed_subtopics,omitempty"`
	StartedAt              int64    `json:"started_at"`
	EndedAt                int64    `json:"ended_at"`
	EndReason              string   `json:"end_reason,omitempty"`
	TurnsCount             int      `json:"turns_count"`
	OffTopicRedirectsCount int      `json:"off_topic_redirects_count"`
	InputTokens            uint64   `json:"input_tokens"`
	OutputTokens           uint64   `json:"output_tokens"`
	CachedInputTokens      uint64   `json:"cached_input_tokens,omitempty"`
	// Modality split of the session tokens; image_input_tokens also counts video
	// input, and text_output_tokens includes the thinking tokens.
	TextInputTokens        uint64 `json:"text_input_tokens,omitempty"`
	AudioInputTokens       uint64 `json:"audio_input_tokens,omitempty"`
	ImageInputTokens       uint64 `json:"image_input_tokens,omitempty"`
	CachedTextInputTokens  uint64 `json:"cached_text_input_tokens,omitempty"`
	CachedAudioInputTokens uint64 `json:"cached_audio_input_tokens,omitempty"`
	CachedImageInputTokens uint64 `json:"cached_image_input_tokens,omitempty"`
	TextOutputTokens       uint64 `json:"text_output_tokens,omitempty"`
	AudioOutputTokens      uint64 `json:"audio_output_tokens,omitempty"`
	ThinkingOutputTokens   uint64 `json:"thinking_output_tokens,omitempty"`
	// Cache write split of the input tokens, billed apart from cached input.
	CacheWriteInputTokens      uint64 `json:"cache_write_input_tokens,omitempty"`
	CacheWriteTextInputTokens  uint64 `json:"cache_write_text_input_tokens,omitempty"`
	CacheWriteAudioInputTokens uint64 `json:"cache_write_audio_input_tokens,omitempty"`
	CacheWriteImageInputTokens uint64 `json:"cache_write_image_input_tokens,omitempty"`
	// Tokens spent by the delegated text model of a live session, priced apart,
	// with the same modality and cache split as the session tokens above.
	DelegationInputTokens                uint64 `json:"delegation_input_tokens,omitempty"`
	DelegationOutputTokens               uint64 `json:"delegation_output_tokens,omitempty"`
	DelegationCachedInputTokens          uint64 `json:"delegation_cached_input_tokens,omitempty"`
	DelegationCacheWriteInputTokens      uint64 `json:"delegation_cache_write_input_tokens,omitempty"`
	DelegationTextInputTokens            uint64 `json:"delegation_text_input_tokens,omitempty"`
	DelegationAudioInputTokens           uint64 `json:"delegation_audio_input_tokens,omitempty"`
	DelegationImageInputTokens           uint64 `json:"delegation_image_input_tokens,omitempty"`
	DelegationCachedTextInputTokens      uint64 `json:"delegation_cached_text_input_tokens,omitempty"`
	DelegationCachedAudioInputTokens     uint64 `json:"delegation_cached_audio_input_tokens,omitempty"`
	DelegationCachedImageInputTokens     uint64 `json:"delegation_cached_image_input_tokens,omitempty"`
	DelegationCacheWriteTextInputTokens  uint64 `json:"delegation_cache_write_text_input_tokens,omitempty"`
	DelegationCacheWriteAudioInputTokens uint64 `json:"delegation_cache_write_audio_input_tokens,omitempty"`
	DelegationCacheWriteImageInputTokens uint64 `json:"delegation_cache_write_image_input_tokens,omitempty"`
	DelegationTextOutputTokens           uint64 `json:"delegation_text_output_tokens,omitempty"`
	DelegationAudioOutputTokens          uint64 `json:"delegation_audio_output_tokens,omitempty"`
	DelegationThinkingOutputTokens       uint64 `json:"delegation_thinking_output_tokens,omitempty"`

	AudioMillis uint64                    `json:"audio_millis"`
	ToolsUsed   map[string]int            `json:"tools_used,omitempty"`
	Transcript  []AISessionTranscriptTurn `json:"transcript,omitempty"`
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
