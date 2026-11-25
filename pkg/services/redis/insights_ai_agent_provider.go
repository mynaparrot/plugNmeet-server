package redisservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// AIAgentCustomProviderConfig is the room's host-supplied provider (BYOK/BYOP).
// It never travels with the room metadata: it is stripped before the metadata is
// published, so the secret lives in Redis only, for the lifetime of the room.
type AIAgentCustomProviderConfig struct {
	Provider        string `json:"provider"`
	APIKey          string `json:"api_key"`
	Endpoint        string `json:"endpoint,omitempty"`
	AuthHeader      string `json:"auth_header,omitempty"`
	RealtimeModel   string `json:"realtime_model,omitempty"`
	DelegationModel string `json:"delegation_model,omitempty"`
	SummarizeModel  string `json:"summarize_model,omitempty"`
	// AvatarProvider, AvatarAPIKey, AvatarEndpoint and AvatarAuthHeader belong
	// to the room's own realtime avatar. They share the key with the LLM
	// provider because both belong to the room and share its lifetime.
	AvatarProvider   string `json:"avatar_provider,omitempty"`
	AvatarAPIKey     string `json:"avatar_api_key,omitempty"`
	AvatarEndpoint   string `json:"avatar_endpoint,omitempty"`
	AvatarAuthHeader string `json:"avatar_auth_header,omitempty"`
}

// aiAgentProviderConfigKey is the room-scoped key of the custom provider. It
// carries a mandatory TTL, set by the caller.
func aiAgentProviderConfigKey(roomId string) string {
	return fmt.Sprintf("%s:%s:provider_config", AiAgentRedisKey, roomId)
}

// SaveAIAgentCustomProvider stores the room's custom provider configuration.
func (s *RedisService) SaveAIAgentCustomProvider(ctx context.Context, roomId string, cfg AIAgentCustomProviderConfig, ttl time.Duration) error {
	encoded, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.rc.Set(ctx, aiAgentProviderConfigKey(roomId), string(encoded), s.resolveTTL(ttl)).Err()
}

// LoadAIAgentCustomProvider returns the stored configuration, or nil when the
// room uses the server-configured provider.
func (s *RedisService) LoadAIAgentCustomProvider(ctx context.Context, roomId string) (*AIAgentCustomProviderConfig, error) {
	raw, err := s.rc.Get(ctx, aiAgentProviderConfigKey(roomId)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	cfg := new(AIAgentCustomProviderConfig)
	if err := json.Unmarshal([]byte(raw), cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// DeleteAIAgentCustomProvider drops the room's credentials, e.g. when the room
// ended or when a reused room id was created without a custom provider.
func (s *RedisService) DeleteAIAgentCustomProvider(ctx context.Context, roomId string) error {
	return s.rc.Del(ctx, aiAgentProviderConfigKey(roomId)).Err()
}

// aiAgentRoomConfigKeys returns the room-scoped keys a child room inherits from
// its parent, in a fixed order so that the values of a single MGet can be mapped
// back to the key they belong to.
func aiAgentRoomConfigKeys(roomId string) []string {
	return []string{
		aiAgentProviderConfigKey(roomId),
		aiAgentMCPConfigKey(roomId),
		aiAgentMCPToolsKey(roomId),
	}
}

// CopyAIAgentRoomConfig copies the server-side AI agent configuration of a
// parent room to its child room: the custom provider credentials plus the
// external MCP servers with their warmed tools. None of it lives in the
// metadata, so a 1-on-1 breakout room would otherwise start without them.
//
// The values are cloned verbatim: one MGet reads all parent keys and a single
// pipeline writes the present ones to the child room with the same TTL, so
// cloning costs one round trip each way and no JSON round trip at all.
func (s *RedisService) CopyAIAgentRoomConfig(ctx context.Context, parentRoomId, childRoomId string, ttl time.Duration) {
	if parentRoomId == "" || childRoomId == "" || parentRoomId == childRoomId {
		return
	}
	ttl = s.resolveTTL(ttl)

	parentKeys := aiAgentRoomConfigKeys(parentRoomId)
	values, err := s.rc.MGet(ctx, parentKeys...).Result()
	if err != nil {
		s.logger.WithError(err).WithField("roomId", childRoomId).Debug("failed to read the ai agent configuration of the parent room")
		return
	}

	childKeys := aiAgentRoomConfigKeys(childRoomId)
	pipe := s.rc.Pipeline()
	copied := 0
	for i, value := range values {
		raw, ok := value.(string)
		if !ok || i >= len(childKeys) {
			// the parent room has no such key, so there is nothing to inherit
			continue
		}
		pipe.Set(ctx, childKeys[i], raw, ttl)
		copied++
	}
	if copied == 0 {
		return
	}

	if _, err = pipe.Exec(ctx); err != nil {
		s.logger.WithError(err).WithField("roomId", childRoomId).Debug("failed to copy the ai agent configuration to the breakout room")
	}
}
