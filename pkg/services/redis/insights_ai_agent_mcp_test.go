package redisservice

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mynaparrot/plugnmeet-protocol/connections"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

func newMCPTestService(t *testing.T) (*RedisService, redis.UniversalClient) {
	t.Helper()
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ttl := 2 * time.Hour
	app := &config.AppConfig{RedisInfo: connections.RedisInfo{DefaultTTL: &ttl}}
	return New(Args{Ctx: context.Background(), Rc: rc, App: app, Logger: logrus.New()}), rc
}

func TestExternalMCPConfigAndToolsRoundTrip(t *testing.T) {
	s, rc := newMCPTestService(t)
	ctx := context.Background()
	ttl := time.Hour

	if servers, err := s.LoadExternalMCPConfig(ctx, "room-a"); err != nil || servers != nil {
		t.Fatalf("a room without config must load nothing (err %v, servers %+v)", err, servers)
	}
	config := map[string]*plugnmeet.ExternalMCPServerConfig{
		"docs": {Url: "https://mcp.example.com/mcp", Headers: map[string]string{"X-Api-Key": "secret"}},
	}
	if err := s.SaveExternalMCPConfig(ctx, "room-a", config, ttl); err != nil {
		t.Fatal(err)
	}
	servers, err := s.LoadExternalMCPConfig(ctx, "room-a")
	if err != nil || len(servers) != 1 || servers["docs"].GetUrl() != config["docs"].GetUrl() {
		t.Fatalf("loaded servers = %+v (err %v)", servers, err)
	}
	if servers["docs"].GetHeaders()["X-Api-Key"] != "secret" {
		t.Error("headers must survive the round trip")
	}
	if left, err := rc.TTL(ctx, aiAgentMCPConfigKey("room-a")).Result(); err != nil || left > ttl {
		t.Errorf("config ttl = %s (err %v), want at most %s", left, err, ttl)
	}

	tools := []*insights.CachedExternalMCPTool{{
		ServerKey:      "docs",
		ServerSlug:     "docs",
		RemoteToolName: "search_docs",
		Spec:           &insights.MCPToolSpec{Name: "ext_docs_search_docs", Description: "Search.", InputSchema: map[string]any{"type": "object"}},
	}}
	if err := s.SaveExternalMCPTools(ctx, "room-a", tools, ttl); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.LoadExternalMCPTools(ctx, "room-a")
	if err != nil || len(loaded) != 1 || loaded[0].Spec == nil || loaded[0].Spec.Name != "ext_docs_search_docs" {
		t.Fatalf("loaded tools = %+v (err %v)", loaded, err)
	}
}

func TestExternalMCPClearScopes(t *testing.T) {
	s, rc := newMCPTestService(t)
	ctx := context.Background()
	ttl := time.Hour

	seed := func(roomId string) {
		t.Helper()
		for _, key := range []string{aiAgentMCPConfigKey(roomId), aiAgentMCPToolsKey(roomId)} {
			if err := rc.Set(ctx, key, "{}", ttl).Err(); err != nil {
				t.Fatal(err)
			}
		}
		if err := rc.HSet(ctx, aiAgentMCPCacheKey(roomId), "field", "value").Err(); err != nil {
			t.Fatal(err)
		}
	}
	exists := func(keys ...string) map[string]bool {
		t.Helper()
		found := map[string]bool{}
		for _, key := range keys {
			n, err := rc.Exists(ctx, key).Result()
			if err != nil {
				t.Fatal(err)
			}
			found[key] = n == 1
		}
		return found
	}

	seed("room-cached")
	if err := s.ClearExternalMCPCachedTools(ctx, "room-cached"); err != nil {
		t.Fatal(err)
	}
	found := exists(aiAgentMCPConfigKey("room-cached"), aiAgentMCPToolsKey("room-cached"), aiAgentMCPCacheKey("room-cached"))
	if !found[aiAgentMCPConfigKey("room-cached")] {
		t.Error("the config must survive ClearExternalMCPCachedTools")
	}
	if found[aiAgentMCPToolsKey("room-cached")] || found[aiAgentMCPCacheKey("room-cached")] {
		t.Error("ClearExternalMCPCachedTools must drop the tools and the cache")
	}

	seed("room-all")
	if err := s.ClearExternalMCPTools(ctx, "room-all"); err != nil {
		t.Fatal(err)
	}
	for key, stillThere := range exists(aiAgentMCPConfigKey("room-all"), aiAgentMCPToolsKey("room-all"), aiAgentMCPCacheKey("room-all")) {
		if stillThere {
			t.Errorf("%s must be cleared", key)
		}
	}
}

func TestExternalMCPCachedOutputCaps(t *testing.T) {
	s, _ := newMCPTestService(t)
	ctx := context.Background()
	ttl := time.Hour
	const maxEntryBytes, maxEntries = 16, 3

	s.SaveExternalMCPCachedOutput(ctx, "room-caps", "big", strings.Repeat("a", maxEntryBytes+1), maxEntryBytes, maxEntries, ttl)
	if _, found := s.LoadExternalMCPCachedOutput(ctx, "room-caps", "big"); found {
		t.Error("oversized entries must not be cached")
	}

	for i := 0; i < maxEntries+5; i++ {
		s.SaveExternalMCPCachedOutput(ctx, "room-caps", string(rune('a'+i)), "value", maxEntryBytes, maxEntries, ttl)
	}
	stored := 0
	for i := 0; i < maxEntries+5; i++ {
		if _, found := s.LoadExternalMCPCachedOutput(ctx, "room-caps", string(rune('a'+i))); found {
			stored++
		}
	}
	if stored != maxEntries {
		t.Errorf("cached entries = %d, want %d", stored, maxEntries)
	}
}

func TestCleanupRoomExternalMCP(t *testing.T) {
	s, rc := newMCPTestService(t)
	ctx := context.Background()
	ttl := time.Hour

	knowledgeKey := fmt.Sprintf("%s:room-clean:kb:doc", AiAgentRedisKey)
	roomKeys := []string{
		aiAgentMCPConfigKey("room-clean"),
		aiAgentMCPToolsKey("room-clean"),
		aiAgentMCPCacheKey("room-clean"),
		knowledgeKey,
	}
	for _, key := range roomKeys[:2] {
		if err := rc.Set(ctx, key, "{}", ttl).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if err := rc.HSet(ctx, aiAgentMCPCacheKey("room-clean"), "field", "value").Err(); err != nil {
		t.Fatal(err)
	}
	if err := rc.Set(ctx, knowledgeKey, "doc", ttl).Err(); err != nil {
		t.Fatal(err)
	}
	otherRoomKey := aiAgentMCPConfigKey("room-keep")
	if err := rc.Set(ctx, otherRoomKey, "{}", ttl).Err(); err != nil {
		t.Fatal(err)
	}

	s.CleanupRoomExternalMCP(ctx, "room-clean")

	for _, key := range roomKeys {
		if n, err := rc.Exists(ctx, key).Result(); err != nil || n != 0 {
			t.Errorf("%s still exists (err %v)", key, err)
		}
	}
	if n, err := rc.Exists(ctx, otherRoomKey).Result(); err != nil || n != 1 {
		t.Errorf("another room's key was removed (err %v)", err)
	}
}
