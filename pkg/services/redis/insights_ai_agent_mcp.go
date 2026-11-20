package redisservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/redis/go-redis/v9"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

// AiAgentRedisKey scopes every room key of the AI agent.
const AiAgentRedisKey = Prefix + "ai_agent"

// Room-scoped keys of the agent's external MCP servers. Every key carries a
// mandatory TTL, set by the caller.
func aiAgentMCPConfigKey(roomId string) string {
	return fmt.Sprintf("%s:%s:mcp_config", AiAgentRedisKey, roomId)
}

func aiAgentMCPToolsKey(roomId string) string {
	return fmt.Sprintf("%s:%s:mcp_tools", AiAgentRedisKey, roomId)
}

func aiAgentMCPCacheKey(roomId string) string {
	return fmt.Sprintf("%s:%s:mcp_cache", AiAgentRedisKey, roomId)
}

// aiAgentKnowledgePattern matches the room's fetched knowledge documents.
func aiAgentKnowledgePattern(roomId string) string {
	return fmt.Sprintf("%s:%s:kb:*", AiAgentRedisKey, roomId)
}

// SaveExternalMCPConfig stores the room's external MCP server configuration.
func (s *RedisService) SaveExternalMCPConfig(ctx context.Context, roomId string, servers map[string]*plugnmeet.ExternalMCPServerConfig, ttl time.Duration) error {
	encoded, err := json.Marshal(servers)
	if err != nil {
		return err
	}
	return s.rc.Set(ctx, aiAgentMCPConfigKey(roomId), string(encoded), s.resolveTTL(ttl)).Err()
}

// LoadExternalMCPConfig returns the stored servers, or nil when the room has
// none.
func (s *RedisService) LoadExternalMCPConfig(ctx context.Context, roomId string) (map[string]*plugnmeet.ExternalMCPServerConfig, error) {
	raw, err := s.rc.Get(ctx, aiAgentMCPConfigKey(roomId)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	servers := map[string]*plugnmeet.ExternalMCPServerConfig{}
	if err := json.Unmarshal([]byte(raw), &servers); err != nil {
		return nil, err
	}
	return servers, nil
}

// SaveExternalMCPTools stores the room's pre-warmed tool descriptions.
func (s *RedisService) SaveExternalMCPTools(ctx context.Context, roomId string, tools []*insights.CachedExternalMCPTool, ttl time.Duration) error {
	encoded, err := json.Marshal(tools)
	if err != nil {
		return err
	}
	return s.rc.Set(ctx, aiAgentMCPToolsKey(roomId), string(encoded), s.resolveTTL(ttl)).Err()
}

// LoadExternalMCPTools returns the pre-warmed tools, or nil when the room has
// none.
func (s *RedisService) LoadExternalMCPTools(ctx context.Context, roomId string) ([]*insights.CachedExternalMCPTool, error) {
	raw, err := s.rc.Get(ctx, aiAgentMCPToolsKey(roomId)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	var tools []*insights.CachedExternalMCPTool
	if err := json.Unmarshal([]byte(raw), &tools); err != nil {
		return nil, err
	}
	return tools, nil
}

// ClearExternalMCPCachedTools drops the discovered tools and their cached
// outputs but keeps the configuration, so a failed discovery can be retried.
func (s *RedisService) ClearExternalMCPCachedTools(ctx context.Context, roomId string) error {
	return s.rc.Del(ctx,
		aiAgentMCPToolsKey(roomId),
		aiAgentMCPCacheKey(roomId),
	).Err()
}

// ClearExternalMCPTools drops the room's external MCP state, e.g. after the
// host removed its servers.
func (s *RedisService) ClearExternalMCPTools(ctx context.Context, roomId string) error {
	return s.rc.Del(ctx,
		aiAgentMCPConfigKey(roomId),
		aiAgentMCPToolsKey(roomId),
		aiAgentMCPCacheKey(roomId),
	).Err()
}

// LoadExternalMCPCachedOutput returns one cached tool output.
func (s *RedisService) LoadExternalMCPCachedOutput(ctx context.Context, roomId, field string) (string, bool) {
	value, err := s.rc.HGet(ctx, aiAgentMCPCacheKey(roomId), field).Result()
	if err != nil {
		return "", false
	}
	return value, true
}

// SaveExternalMCPCachedOutput stores one cached tool output, respecting the
// per-entry size and per-room entry caps.
func (s *RedisService) SaveExternalMCPCachedOutput(ctx context.Context, roomId, field, value string, maxEntryBytes, maxEntries int, ttl time.Duration) {
	if len(value) > maxEntryBytes {
		return
	}
	key := aiAgentMCPCacheKey(roomId)
	if size, err := s.rc.HLen(ctx, key).Result(); err == nil && size >= int64(maxEntries) {
		return
	}
	if err := s.rc.HSet(ctx, key, field, value).Err(); err != nil {
		return
	}
	_ = s.rc.Expire(ctx, key, s.resolveTTL(ttl)).Err()
}

// CleanupRoomExternalMCP drops every external MCP key of a room, including the
// fetched knowledge documents. Quiz answer keys and learning progress are left
// to their TTL: they belong to the session, not to the MCP servers.
func (s *RedisService) CleanupRoomExternalMCP(ctx context.Context, roomId string) {
	if roomId == "" {
		return
	}
	keys := []string{
		aiAgentMCPConfigKey(roomId),
		aiAgentMCPToolsKey(roomId),
		aiAgentMCPCacheKey(roomId),
	}
	var cursor uint64
	for {
		batch, next, err := s.rc.Scan(ctx, cursor, aiAgentKnowledgePattern(roomId), 100).Result()
		if err != nil {
			break
		}
		keys = append(keys, batch...)
		if next == 0 {
			break
		}
		cursor = next
	}
	_ = s.rc.Del(ctx, keys...).Err()
}
