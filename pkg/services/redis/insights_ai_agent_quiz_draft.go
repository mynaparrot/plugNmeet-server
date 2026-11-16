package redisservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

// AiAgentQuizDraftTTL bounds how long a quiz preview waits for approval: the
// host approves it in the chat right away, so a forgotten draft must not
// survive the session.
const AiAgentQuizDraftTTL = 30 * time.Minute

func aiAgentQuizDraftKey(roomId, draftId string) string {
	return fmt.Sprintf("%s:%s:quiz_draft:%s", AiAgentRedisKey, roomId, draftId)
}

// SaveAIQuizDraft stores one quiz preview until it is published or expires.
func (s *RedisService) SaveAIQuizDraft(ctx context.Context, roomId, draftId string, draft *insights.AIQuizDraft) error {
	if draft == nil {
		return nil
	}
	encoded, err := json.Marshal(draft)
	if err != nil {
		return err
	}
	return s.rc.Set(ctx, aiAgentQuizDraftKey(roomId, draftId), encoded, AiAgentQuizDraftTTL).Err()
}

// GetAIQuizDraft returns a stored quiz preview; found is false when it was
// already published, replaced or expired.
func (s *RedisService) GetAIQuizDraft(ctx context.Context, roomId, draftId string) (*insights.AIQuizDraft, bool, error) {
	encoded, err := s.rc.Get(ctx, aiAgentQuizDraftKey(roomId, draftId)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, false, nil
		}
		return nil, false, err
	}
	draft := new(insights.AIQuizDraft)
	if err := json.Unmarshal(encoded, draft); err != nil {
		return nil, false, err
	}
	return draft, true, nil
}

// DeleteAIQuizDraft consumes a preview, so the same draft can never be
// published twice.
func (s *RedisService) DeleteAIQuizDraft(ctx context.Context, roomId, draftId string) error {
	return s.rc.Del(ctx, aiAgentQuizDraftKey(roomId, draftId)).Err()
}
